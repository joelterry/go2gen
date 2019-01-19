package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"strconv"

	"github.com/go-toolsmith/astcopy"
	"golang.org/x/tools/go/ast/astutil"
)

type go2File struct {
	name string
	fset *token.FileSet
	f    *ast.File
	checkMap
	handleMap

	original *ast.File
}

func (gf go2File) pos(node ast.Node) token.Pos {
	return node.Pos() - gf.f.Pos() + 1
}

func (gf go2File) string() (string, error) {
	var buf bytes.Buffer
	err := format.Node(&buf, gf.fset, gf.f)
	if err != nil {
		return "", err
	}
	str := buf.String()
	f, err := parseString(str)
	if err != nil {
		return "", err
	}

	funcPositions := make(map[token.Pos]token.Pos) // beginning -> end
	astutil.Apply(f, func(c *astutil.Cursor) bool {
		node := c.Node()
		if node == nil {
			return true
		}
		if isFunc(node) {
			funcPositions[node.Pos()] = node.End()
		}
		return true
	}, nil)

	var cs cuts
	for start, end := range funcPositions {
		s, e := int(start-1), int(end-1)
		var trailing []int
		newlineFound := false
		for i := s; i < e; i++ {
			if str[i] != '\n' {
				newlineFound = false
				if len(trailing) > 0 {
					cs = append(cs, cut{
						start: trailing[0],
						end:   trailing[len(trailing)-1] + 1,
					})
					trailing = nil
				}
				continue
			}
			if newlineFound {
				trailing = append(trailing, i)
				continue
			}
			newlineFound = true
		}
	}

	return cs.Apply(str)
}

// for (func|block|stmt)Tree, (func|block|stmt)s point to themselves
type funcTree map[ast.Node]ast.Node // node => func
type blockTree map[ast.Node]*ast.BlockStmt
type exprTree map[ast.Expr]ast.Stmt
type stmtTree map[ast.Stmt]ast.Stmt
type blockIsFunc map[*ast.BlockStmt]bool
type scopeMap map[*ast.BlockStmt]map[string]int

type treeInfo struct {
	funcTree
	blockTree
	exprTree
	blockIsFunc
	scopeMap
}

type checkInfo struct {
	fun         ast.Node
	block       *ast.BlockStmt
	stmt        ast.Stmt
	scope       map[string]int
	handleChain []*ast.BlockStmt
}

func buildTreeInfo(root ast.Node) treeInfo {

	info := treeInfo{
		funcTree:    make(funcTree),
		blockTree:   make(blockTree),
		exprTree:    make(exprTree),
		blockIsFunc: make(blockIsFunc),
		scopeMap:    make(scopeMap),
	}

	astutil.Apply(root, func(c *astutil.Cursor) bool {

		node := c.Node()
		if node == nil {
			return true
		}

		if block, ok := node.(*ast.BlockStmt); ok {
			info.scopeMap[block] = make(map[string]int)
		}

		parent := c.Parent()
		if parent == nil {
			return true
		}

		if isFunc(parent) {
			info.funcTree[node] = parent
			if funcBlock, ok := node.(*ast.BlockStmt); ok {
				// I think this would always be true
				info.blockIsFunc[funcBlock] = true
			}
		} else {
			info.funcTree[node] = info.funcTree[parent]
		}

		if block, ok := parent.(*ast.BlockStmt); ok {
			info.blockTree[node] = block
		} else {
			info.blockTree[node] = info.blockTree[parent]
		}

		expr, ok := node.(ast.Expr)
		if !ok {
			return true
		}

		switch v := parent.(type) {
		case ast.Stmt:
			info.exprTree[expr] = v
		case ast.Expr:
			info.exprTree[expr] = info.exprTree[v]
		}

		return true
	}, nil)

	return info
}

func lexicalStmtTree(root ast.Node, info treeInfo) stmtTree {

	stmtLists := make(map[*ast.BlockStmt][]ast.Stmt)

	astutil.Apply(root, func(c *astutil.Cursor) bool {

		node := c.Node()
		if node == nil {
			return true
		}

		stmt, ok := node.(ast.Stmt)
		if !ok {
			return true
		}

		parentBlock := info.blockTree[node]
		if parentBlock == nil {
			return true
		}
		pl := stmtLists[parentBlock]

		block, ok := node.(*ast.BlockStmt)
		if ok {
			newList := make([]ast.Stmt, len(pl))
			copy(newList, pl)
			stmtLists[block] = newList
		}

		stmtLists[parentBlock] = append(pl, stmt)

		return true
	}, nil)

	st := make(stmtTree)
	for _, l := range stmtLists {
		for i, stmt := range l {
			if i == 0 {
				continue
			}
			st[stmt] = l[i-1]
		}
	}

	return st
}

func collectChecksAndHandles2(gf *go2File, handlerErrNames map[*ast.BlockStmt]string) []ast.Expr {

	var checks []ast.Expr
	checkCollected := make(map[token.Pos]bool)

	astutil.Apply(gf.f, func(c *astutil.Cursor) bool {

		node := c.Node()
		if node == nil {
			return true
		}
		pos := gf.pos(node)

		if errName, ok := gf.handleMap[pos]; ok {
			block, ok := node.(*ast.BlockStmt)
			if !ok {
				panic(fmt.Errorf("handle not a block: %#v", node))
			}
			handlerErrNames[block] = errName
			c.Delete()
			return false
		}

		if gf.checkMap[pos] {
			expr, ok := node.(ast.Expr)
			if !ok {
				// should this be an error?
				return true
			}
			// filter out non-unary expressions
			switch expr.(type) {
			case *ast.BinaryExpr, *ast.KeyValueExpr:
				return true
			}

			// ensure we don't get duplicates for the same pos
			// for example: check fn(), we only want fn(), not the identifier "fn" as well
			if checkCollected[pos] {
				return true
			}
			checkCollected[pos] = true
			checks = append(checks, expr)
		}

		return true
	}, nil)

	return checks
}

type transformContext struct {
	checks          map[ast.Expr]checkInfo
	toDelete        map[ast.Node]bool
	handlerErrNames map[*ast.BlockStmt]string
}

func newTransformContext() transformContext {
	return transformContext{
		checks:          make(map[ast.Expr]checkInfo),
		toDelete:        make(map[ast.Node]bool),
		handlerErrNames: make(map[*ast.BlockStmt]string),
	}
}

func (tc transformContext) buildHandlerChains(gf *go2File, info treeInfo, st stmtTree, checks []ast.Expr) {

	for _, c := range checks {
		var chain []*ast.BlockStmt

		stmt := info.exprTree[c]
		for stmt != nil {
			pos := gf.pos(stmt)
			_, ok := gf.handleMap[pos]
			if ok {
				block, ok := stmt.(*ast.BlockStmt)
				if !ok {
					panic(fmt.Errorf("handle not a block: %#v", stmt))
				}
				chain = append(chain, block)
			}
			stmt = st[stmt]
		}

		block := info.blockTree[c]
		tc.checks[c] = checkInfo{
			fun:         info.funcTree[c],
			block:       block,
			stmt:        info.exprTree[c],
			scope:       info.scopeMap[block],
			handleChain: chain,
		}
	}
}

func (tc transformContext) consumeTypedChecks(gf *go2File, info *types.Info) {

	stmtInterrupted := make(map[ast.Stmt]bool)

	astutil.Apply(gf.f, nil, func(c *astutil.Cursor) bool {

		node := c.Node()
		if node == nil {
			return true
		}

		expr, ok := node.(ast.Expr)
		if !ok {
			return true
		}

		checkInfo, ok := tc.checks[expr]
		if !ok || stmtInterrupted[checkInfo.stmt] {
			return true
		}

		t := info.TypeOf(expr)
		if t == nil {
			stmtInterrupted[checkInfo.stmt] = true
			return true
		}

		delete(tc.checks, expr)

		var names []string

		switch v := t.(type) {
		case *types.Named:
			names = []string{"error"}
		case *types.Tuple:
			names = make([]string, v.Len())
			for i := 0; i < v.Len(); i++ {
				names[i] = typeToVar(v.At(i).Type().String())
			}
		default:
			panic(fmt.Errorf("return type must be Named or Tuple: %#v", v))
		}

		if len(names) == 0 {
			panic(errors.New("check expression evaluates to no values"))
		}

		if names[len(names)-1] != "error" {
			panic(errors.New("lats value of check expression must be error"))
		}

		for i, name := range names {
			names[i] = varPrefix + name + strconv.Itoa(checkInfo.scope[name])
			checkInfo.scope[name]++
		}

		errName := names[len(names)-1]

		switch v := c.Parent().(type) {
		case *ast.CallExpr:
			if len(names) > 2 {
				args := names[0 : len(names)-1]
				v.Args = toIdentExprs(args)
			} else {
				c.Replace(ast.NewIdent(names[0]))
			}
		case *ast.AssignStmt:
			c.Replace(ast.NewIdent(names[0]))
		case *ast.ExprStmt:
			tc.toDelete[v] = true
			for i := range names[0 : len(names)-1] {
				names[i] = "_"
			}
		case ast.Expr:
			if len(names) < 2 {
				panic(errors.New("check expression's parent must be call or assignment to have multiple values"))
			}
			c.Replace(ast.NewIdent(names[0]))
		}

		var hl []ast.Stmt
		for _, handler := range checkInfo.handleChain {
			h := astcopy.BlockStmt(handler)
			replaceIdent(h, tc.handlerErrNames[handler], errName)
			hl = append(hl, h.List...)
		}
		handleBody := &ast.BlockStmt{List: hl}
		trimTerminatingStatements(handleBody)

		genAssign := &ast.AssignStmt{
			Lhs: toIdentExprs(names),
			Tok: token.DEFINE,
			Rhs: []ast.Expr{expr}, // is this copy alright?
		}
		genIf := &ast.IfStmt{
			Cond: &ast.BinaryExpr{
				X:  ast.NewIdent(names[len(names)-1]),
				Op: token.NEQ,
				Y:  ast.NewIdent("nil"),
			},
			Body: handleBody,
		}

		cb := checkInfo.block
		for i, stmt := range cb.List {
			if stmt == checkInfo.stmt {
				cb.List = append(
					cb.List[0:i],
					append(
						[]ast.Stmt{genAssign, genIf},
						cb.List[i:]...,
					)...,
				)
				break
			}
		}

		fmt.Println(names)

		return true
	})
}

func (tc transformContext) deleteExprStmts(gf *go2File) {
	astutil.Apply(gf.f, func(c *astutil.Cursor) bool {
		node := c.Node()
		if node == nil {
			return true
		}
		exprStmt, ok := node.(*ast.ExprStmt)
		if !ok {
			return true
		}
		if !tc.toDelete[exprStmt] {
			return true
		}
		c.Delete()
		delete(tc.toDelete, exprStmt)
		return true
	}, nil)
}

func transform2(p *go2Package) error {

	tc := newTransformContext()

	for _, gf := range p.go2Files {
		ti := buildTreeInfo(gf.f)
		lst := lexicalStmtTree(gf.f, ti)
		checks := collectChecksAndHandles2(gf, tc.handlerErrNames)
		tc.buildHandlerChains(gf, ti, lst, checks)
	}

	prevRemaining := len(tc.checks)
	for {
		fmt.Println("----------")

		info, err := p.checkTypes()
		if err != nil {
			return err
		}

		for _, gf := range p.go2Files {
			tc.consumeTypedChecks(gf, info)
			tc.deleteExprStmts(gf)
		}

		if len(tc.checks) == 0 {
			break
		}
		if len(tc.checks) == prevRemaining {
			fmt.Println("UNDEFINED TYPE")
			break
		}
		prevRemaining = len(tc.checks)
	}
	return nil
}

// for debugging
func nodeString(node ast.Node) string {
	fset := token.NewFileSet()
	var buf bytes.Buffer
	err := format.Node(&buf, fset, node)
	if err != nil {
		panic(err)
	}
	return buf.String()
}
