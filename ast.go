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
	"strings"

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

type global struct {
	ft     funcTree
	bt     blockTree
	scopes scopeMap

	hcs            handleChains
	handleErrNames map[*ast.BlockStmt]string
	snh            stmtNumHandlers
	ces            map[ast.Expr]checkLocation

	delExprStmts map[*ast.ExprStmt]bool
}

type funcTree map[ast.Node]ast.Node // node => func
type blockTree map[ast.Node]*ast.BlockStmt
type handleChains map[ast.Node][]*ast.BlockStmt // func => []block
type stmtNumHandlers map[ast.Stmt]int
type checkExprs map[checkLocation][]ast.Expr
type checkLocation struct {
	fun   ast.Node
	block *ast.BlockStmt
	stmt  ast.Stmt
}
type scopeMap map[*ast.BlockStmt]map[string]int

type context struct {
	*go2File
	global
}

func (ctx context) buildTrees() {
	astutil.Apply(ctx.f, func(c *astutil.Cursor) bool {
		node := c.Node()
		if node == nil {
			return true
		}
		if isFunc(node) {
			ctx.ft[node] = node
			ctx.bt[node] = ctx.bt[c.Parent()]
			return true
		}
		if block, ok := node.(*ast.BlockStmt); ok {
			ctx.ft[node] = ctx.ft[c.Parent()]
			ctx.bt[node] = block
			ctx.scopes[block] = make(map[string]int)
			return true
		}
		ctx.ft[node] = ctx.ft[c.Parent()]
		ctx.bt[node] = ctx.bt[c.Parent()]
		return true
	}, nil)
}

func (ctx context) collectHandlersAndChecks() {

	// nested stmts aren't possible, so pre-built tree not necesssary
	var currStmt ast.Stmt

	checkCollected := make(map[token.Pos]bool)

	astutil.Apply(ctx.f, func(c *astutil.Cursor) bool {
		node := c.Node()
		if node == nil {
			return true
		}

		pos := ctx.pos(node)
		currFunc := ctx.ft[node]
		currBlock := ctx.bt[node]
		if currFunc == nil || currBlock == nil {
			return true
		}

		if errName, ok := ctx.handleMap[pos]; ok {
			block, ok := node.(*ast.BlockStmt)
			if !ok {
				panic(errors.New("a block must follow handle keyword and error identifier"))
			}
			ctx.hcs[currFunc] = append(ctx.hcs[currFunc], block)
			ctx.handleErrNames[block] = errName
			c.Delete()
			return false
		}

		stmt, ok := node.(ast.Stmt)
		if ok {
			ctx.snh[stmt] = len(ctx.hcs[currFunc]) // does not include default handler
			currStmt = stmt
			return true
		}

		if ctx.checkMap[pos] {
			if currStmt == nil {
				panic("check expression found outside stmt... shoudln't be possible")
			}

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
			// for example: check fn(), we only want fn(), not the identifier fn as well
			if checkCollected[pos] {
				return true
			}
			checkCollected[pos] = true

			loc := checkLocation{
				fun:   currFunc,
				block: currBlock,
				stmt:  currStmt,
			}

			ctx.ces[expr] = loc

			return true
		}

		return true
	}, nil)
}

func (ctx context) consumeTypedChecks(info *types.Info) int {

	interruptedStmts := make(map[ast.Stmt]bool)

	postorder := func(c *astutil.Cursor) bool {
		node := c.Node()

		expr, ok := node.(ast.Expr)
		if !ok {
			return true
		}

		loc, ok := ctx.ces[expr]

		if !ok || interruptedStmts[loc.stmt] {
			return true
		}

		t := info.TypeOf(expr)
		if t == nil {
			interruptedStmts[loc.stmt] = true
			return true
		}

		delete(ctx.ces, expr)

		var names []string

		switch v := t.(type) {
		case *types.Named:
			names = []string{"error"}
		case *types.Tuple:
			names = make([]string, v.Len())
			for i := 0; i < v.Len(); i++ {
				names[i] = sanitizeType(v.At(i).Type().String())
			}
		default:
			panic(errors.New("return type must be Named or Tuple"))
		}

		if len(names) == 0 {
			panic(errors.New("check expression evaluates to no values"))
		}

		if names[len(names)-1] != "error" {
			panic(errors.New("last value of check expression must be error"))
		}

		//fmt.Printf("%#v\n", loc)
		//fmt.Println(len(ctx.scopes))
		scope := ctx.scopes[loc.block]
		if scope != nil {
			for i, name := range names {
				names[i] = varPrefix + name + strconv.Itoa(scope[name])
				scope[name]++
			}
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
			ctx.delExprStmts[v] = true
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
		handlers := ctx.hcs[loc.fun]
		numHandlers := ctx.snh[loc.stmt]
		for i := numHandlers - 1; i >= 0; i-- {
			h := astcopy.BlockStmt(handlers[i])
			replaceIdent(h, ctx.handleErrNames[handlers[i]], errName)
			hl = append(hl, h.List...)
		}
		defaultHandler := defaultHandleStmt2(loc.fun, info)
		replaceIdent(defaultHandler, "err", errName)
		hl = append(hl, defaultHandler)
		//fmt.Printf("%#v\n\n\n", defaultHandleStmt2(loc.fun, info))
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

		for i, stmt := range loc.block.List {
			if stmt == loc.stmt {
				loc.block.List = append(
					loc.block.List[0:i],
					append(
						[]ast.Stmt{genAssign, genIf},
						loc.block.List[i:]...,
					)...,
				)
				break
			}
		}

		fmt.Println(names)

		return true
	}

	astutil.Apply(ctx.f, nil, postorder)
	return len(ctx.ces)
}

func (ctx context) deleteExprStmts() {
	astutil.Apply(ctx.f, func(c *astutil.Cursor) bool {
		node := c.Node()
		if node == nil {
			return true
		}
		exprStmt, ok := node.(*ast.ExprStmt)
		if !ok {
			return true
		}
		if !ctx.delExprStmts[exprStmt] {
			return true
		}
		c.Delete()
		delete(ctx.delExprStmts, exprStmt)
		return true
	}, nil)
}

func transform(p *go2Package) error {
	g := global{
		ft:             make(funcTree),
		bt:             make(blockTree),
		hcs:            make(handleChains),
		handleErrNames: make(map[*ast.BlockStmt]string),
		snh:            make(stmtNumHandlers),
		ces:            make(map[ast.Expr]checkLocation),
		scopes:         make(scopeMap),
		delExprStmts:   make(map[*ast.ExprStmt]bool),
	}

	for _, gf := range p.go2Files {
		ctx := context{
			go2File: gf,
			global:  g,
		}
		ctx.buildTrees()
	}

	for _, gf := range p.go2Files {
		ctx := context{
			go2File: gf,
			global:  g,
		}
		ctx.collectHandlersAndChecks()
	}

	prevRemaining := -1
	for {
		fmt.Println("--------")

		info, err := p.checkTypes()
		if err != nil {
			return err
		}

		remaining := 0
		for _, gf := range p.go2Files {
			ctx := context{
				go2File: gf,
				global:  g,
			}
			remaining += ctx.consumeTypedChecks(info)
			ctx.deleteExprStmts()
		}

		if remaining == 0 {
			break
		}
		if remaining == prevRemaining {
			fmt.Println("UNDEFINED TYPE")
			break
			//return errors.New("UNDEFINED TYPE")
		}
		prevRemaining = remaining
	}

	return nil
}

func isFunc(node ast.Node) bool {
	switch node.(type) {
	case *ast.FuncDecl:
		return true
	case *ast.FuncLit:
		return true
	default:
		return false
	}
}

func toIdentExprs(names []string) []ast.Expr {
	idents := make([]ast.Expr, len(names))
	for i, name := range names {
		idents[i] = &ast.Ident{Name: name}
	}
	return idents
}

/*
func nodeString(node ast.Node) (string, error) {
	var buf bytes.Buffer
	err := ast.Fprint(&buf, node, nil)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}
*/

// fun must be *ast.FuncDecl or *ast.FuncLit
func defaultHandleStmt2(fun ast.Node, info *types.Info) ast.Stmt {

	var ft *ast.FuncType
	switch v := fun.(type) {
	case *ast.FuncDecl:
		ft = v.Type
	case *ast.FuncLit:
		ft = v.Type
	default:
		panic("fun must be *ast.FuncDecl or *ast.FuncLit")
	}

	var ftrl []*ast.Field
	if ft.Results != nil {
		ftrl = ft.Results.List
	}
	if len(ftrl) == 0 {
		return panicWithErrStmt("err")
	}

	last := ftrl[len(ftrl)-1]
	lastIdent, ok := last.Type.(*ast.Ident)
	if !ok || lastIdent.Name != "error" {
		return panicWithErrStmt("err")
	}

	resultList := make([]ast.Expr, len(ft.Results.List))
	for i, field := range ft.Results.List {
		if i < len(resultList)-1 {
			resultList[i] = &ast.Ident{
				Name: zeroValueString(field.Type, info),
			}
		} else {
			resultList[i] = &ast.Ident{
				Name: "err",
			}
		}
	}

	return &ast.ReturnStmt{
		Results: resultList,
	}
}

func panicWithErrStmt(errVar string) *ast.ExprStmt {
	return &ast.ExprStmt{
		X: &ast.CallExpr{
			Fun: &ast.Ident{Name: "panic"},
			Args: []ast.Expr{
				&ast.Ident{Name: errVar},
			},
		},
	}
}

// https://golang.org/ref/spec#Terminating_statements
// 5-8 (for, switch, select, labels) are currently not implemented.
//
// returns true if stmt is terminating
func trimTerminatingStatements(stmt ast.Stmt) bool {
	if stmt == nil {
		return false
	}
	switch v := stmt.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.ExprStmt:
		callExpr, ok := v.X.(*ast.CallExpr)
		if !ok {
			return false
		}
		ident, ok := callExpr.Fun.(*ast.Ident)
		if !ok {
			return false
		}
		return ident.Name == "panic"
	case *ast.BlockStmt:
		for i, child := range v.List {
			if trimTerminatingStatements(child) {
				v.List = v.List[0 : i+1]
				return true
			}
		}
		return false
	case *ast.IfStmt:
		ifTerm := trimTerminatingStatements(v.Body)
		elseTerm := trimTerminatingStatements(v.Else)
		return ifTerm && elseTerm
	default:
		return false
	}
}

func zeroValueString(typeExpr ast.Expr, info *types.Info) string {
	t := info.TypeOf(typeExpr)
	switch v := t.Underlying().(type) {
	case *types.Basic:
		switch v.Info() {
		case types.IsBoolean:
			return "false"
		case types.IsString:
			return `""`
		default:
			return "0"
		}
	case *types.Struct:
		if ident, ok := typeExpr.(*ast.Ident); ok {
			return ident.Name + "{}"
		}
		if selector, ok := typeExpr.(*ast.SelectorExpr); ok {
			pkg, ok := selector.X.(*ast.Ident)
			if !ok {
				panic("struct type ast.SelectorExpr.X should be Ident (package name)")
			}
			return pkg.Name + "." + selector.Sel.Name + "{}"
		}
		if _, ok := typeExpr.(*ast.StructType); ok {
			return v.String() + "{}"
		}
		panic(errors.New("struct type's corresponding ast expr should only be Ident or SelectorExpr"))
	default:
		return "nil"
	}
}

func replaceIdent(root ast.Node, old string, new string) {
	ast.Inspect(root, func(node ast.Node) bool {
		if ident, ok := node.(*ast.Ident); ok && ident.Name == old {
			ident.Name = new
		}
		return true
	})
}

// replaces *, [], and [N] from type name
func sanitizeType(s string) string {
	s = strings.Replace(s, ".", "", -1)
	s = strings.Replace(s, "*", "pointer", -1)
	var result string
	for {
		l := strings.Index(s, "[")
		if l < 0 {
			break
		}
		r := strings.Index(s, "]")
		if r < 0 {
			panic("sanitizeType: mismatched delimiters")
		}

		result += s[:l]
		if r > l+1 {
			result += "array"
		} else {
			result += "slice"
		}
		s = s[r+1:]
	}
	return result + s
}
