package main

import (
	"errors"
	"fmt"
	"go/token"
	"go/types"
	"strconv"

	ast "github.com/dave/dst"
	"github.com/dave/dst/decorator"
)

// exprList is needed in the case of multiple return values
type exprList []ast.Expr

func (el exprList) Expr() ast.Expr {
	// does this case make sense?
	if len(el) == 0 {
		return nil
	}
	if len(el) == 1 {
		return el[0]
	}
	panic(errors.New("error: multiple values in context where only one is allowed"))
}

type astInfo struct {
	fset *token.FileSet
	info *types.Info
	dec  *decorator.Decorator
}

type astContext struct {
	astInfo
	checkMap
	handleMap
	filePos  token.Pos
	stack    [][]ast.Stmt
	varTypes map[string]int
}

func (ac astContext) withFunction(ft *ast.FuncType) astContext {
	ac.stack = [][]ast.Stmt{
		[]ast.Stmt{defaultHandleStmt(ft, ac.astInfo)},
	}
	return ac
}

func (ac astContext) withHandler(block *ast.BlockStmt) astContext {
	ac.stack = append(ac.stack, block.List)
	return ac
}

func (ac astContext) withScope() astContext {
	ac.varTypes = make(map[string]int)
	return ac
}

func (ac astContext) evalHandleChain(errName string) *ast.BlockStmt {
	block := &ast.BlockStmt{}
	for i := len(ac.stack) - 1; i >= 0; i-- {
		block.List = append(block.List, ac.stack[i]...)
	}

	blockCopy := copyBlockDst(block)
	replaceIdent(blockCopy, handleErr, errName)
	trimTerminatingStatements(blockCopy)
	return blockCopy
}

func (ac astContext) convertFile(f *ast.File, cm checkMap, hm handleMap) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = r.(error)
		}
	}()

	ac.checkMap = cm
	ac.handleMap = hm
	ac.filePos = ac.nodePosDst(f)

	for _, decl := range f.Decls {
		switch v := decl.(type) {
		case *ast.FuncDecl:
			ac.convertFunc(v.Type, v.Body)
		case *ast.GenDecl:
			if v.Tok != token.VAR {
				continue
			}
			for _, spec := range v.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, value := range valueSpec.Values {
					funcLit, ok := value.(*ast.FuncLit)
					if !ok {
						continue
					}
					ac.convertFunc(funcLit.Type, funcLit.Body)
				}
			}
		}
	}

	return
}

func (ac astContext) convertFunc(ft *ast.FuncType, body *ast.BlockStmt) {
	ac = ac.withFunction(ft)
	ac.convertBlock(body)
}

func (ac astContext) convertBlock(block *ast.BlockStmt) {
	ac = ac.withScope()
	var newList []ast.Stmt
	for _, stmt := range block.List {
		handleBlock, ok := ac.toHandleBlock(stmt)
		if ok {
			ac = ac.withHandler(handleBlock)
			continue
		}
		generated := ac.convertStmt(stmt)
		newList = append(newList, generated...)
		newList = append(newList, stmt)
	}
	block.List = newList
}

// Unlike the above convert* funcs, convertStmt and convertExprs
// return []ast.Stmt. This is the generated code, and is passed
// to convertBlock, since that's where the insertion will take place.

// Collection of statements found here:
// https://golang.org/src/go/ast/ast.go#L547
//
// -- supported statements:
// DeclStmt
// ExprStmt
// SendStmt
// AssignStmt
// ReturnStmt
// BlockStmt
//
// -- partially supported statements (only blocks):
// IfStmt (init/cond for "if" COULD be supported, but not "if else")
// CaseClause
// SwitchStmt
// TypeSwitchStmt
// CommClause
// SelectStmt
// ForStmt
// RangeStmt
//
// -- unsupported statements:
// BadStmt
// EmptyStmt
// GoStmt (can't evaluate to a function call)
// DeferStmt (https://go.googlesource.com/proposal/+/master/design/go2draft-error-handling.md#checking-error-returns-from-deferred-calls)
// BranchStmt
//
// -- maybe:
// LabeledStmt (probably not, don't understand it well enough)
// IncDecStmt (I thikn it would require changing to assignment)
func (ac astContext) convertStmt(stmt ast.Stmt) []ast.Stmt {
	switch v := stmt.(type) {
	case *ast.DeclStmt:
		genDecl, ok := v.Decl.(*ast.GenDecl)
		if !ok {
			return nil
		}
		switch genDecl.Tok {
		case token.VAR, token.CONST:
			for _, spec := range genDecl.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				gen, newExprs := ac.convertExprs(valueSpec.Values)
				valueSpec.Values = newExprs
				return gen
			}
		default:
			return nil
		}
	case *ast.ExprStmt:
		gen, newExpr := ac.convertExpr(v.X)
		v.X = newExpr
		return gen
	case *ast.SendStmt:
		// v.Chan too?
		gen, newExpr := ac.convertExpr(v.Value)
		v.Value = newExpr
		return gen
	case *ast.AssignStmt:
		gen, newExprs := ac.convertExprs(v.Rhs)
		v.Rhs = newExprs
		return gen
	case *ast.ReturnStmt:
		gen, newExprs := ac.convertExprs(v.Results)
		v.Results = newExprs
		return gen
	case *ast.BlockStmt:
		ac.convertBlock(v)

	case *ast.IfStmt:
		ac.convertBlock(v.Body)
		ac.convertStmt(v.Else)
	case *ast.CaseClause:
		for _, s := range v.Body {
			ac.convertStmt(s)
		}
	case *ast.SwitchStmt:
		ac.convertBlock(v.Body)
	case *ast.TypeSwitchStmt:
		ac.convertBlock(v.Body)
	case *ast.CommClause:
		for _, s := range v.Body {
			ac.convertStmt(s)
		}
	case *ast.SelectStmt:
		ac.convertBlock(v.Body)
	case *ast.ForStmt:
		ac.convertBlock(v.Body)
	case *ast.RangeStmt:
		ac.convertBlock(v.Body)
	default:
		return nil
	}
	return nil
}

// returns generated code and altered expression list
//
// this is the only place where an exprList returned from convertCheck
// isn't automatically "cast" to an Expr
func (ac astContext) convertExprs(exprs []ast.Expr) ([]ast.Stmt, []ast.Expr) {
	var generated []ast.Stmt
	var newExprs []ast.Expr

	for _, expr := range exprs {
		if ac.isCheckExpr(expr) {
			stmts, newExprList := ac.convertCheck(expr)
			if len(newExprList) > 1 {
				if len(exprs) > 1 {
					panic(errors.New("error: multiple values in context where only one is allowed"))
				}
				return stmts, newExprList
			}
			generated = append(generated, stmts...)
			newExprs = append(newExprs, newExprList...)
			continue
		}
		stmts, newExpr := ac.convertExpr(expr)
		generated = append(generated, stmts...)
		newExprs = append(newExprs, newExpr)
	}

	return generated, newExprs
}

// returns generated code and altered expression
//
// This is the messiest convert* function, given the intense switch statement.
// An astutil traversal (https://godoc.org/golang.org/x/tools/go/ast/astutil#Apply)
// would likely have much less code, since I could traverse and replace nodes
// generically. However, it would also make it harder to reason about the problem recursively.
func (ac astContext) convertExpr(expr ast.Expr) ([]ast.Stmt, ast.Expr) {
	if expr == nil {
		return nil, nil
	}

	// function literal is special case: start new conversion
	if funcLit, ok := expr.(*ast.FuncLit); ok {
		ac.convertFunc(funcLit.Type, funcLit.Body)
		return nil, expr
	}

	if ac.isCheckExpr(expr) {
		gen, newExprList := ac.convertCheck(expr)
		return gen, newExprList.Expr()
	}

	// https://golang.org/src/go/ast/ast.go#L227
	switch v := expr.(type) {
	case *ast.CompositeLit:
		gen, newExprs := ac.convertExprs(v.Elts)
		v.Elts = newExprs
		return gen, v
	case *ast.ParenExpr:
		gen, newExpr := ac.convertExpr(v.X)
		v.X = newExpr
		return gen, v
	case *ast.SelectorExpr:
		gen, newExpr := ac.convertExpr(v.X)
		v.X = newExpr
		return gen, v
	case *ast.IndexExpr:
		var gens []ast.Stmt

		gen, newExpr := ac.convertExpr(v.X)
		gens = append(gens, gen...)
		v.X = newExpr

		gen, newExpr = ac.convertExpr(v.Index)
		gens = append(gens, gen...)
		v.Index = newExpr

		return gens, v
	case *ast.SliceExpr:
		var gens []ast.Stmt

		gen, newExpr := ac.convertExpr(v.Low)
		gens = append(gens, gen...)
		v.Low = newExpr

		gen, newExpr = ac.convertExpr(v.High)
		gens = append(gens, gen...)
		v.High = newExpr

		gen, newExpr = ac.convertExpr(v.Max)
		gens = append(gens, gen...)
		v.Max = newExpr

		return gens, v
	case *ast.TypeAssertExpr:
		gen, newExpr := ac.convertExpr(v.X)
		v.X = newExpr
		return gen, v
	case *ast.CallExpr:
		var gens []ast.Stmt

		gen, newExpr := ac.convertExpr(v.Fun)
		gens = append(gens, gen...)
		v.Fun = newExpr

		gen, newExprs := ac.convertExprs(v.Args)
		gens = append(gens, gen...)
		v.Args = newExprs
		return gen, v
	case *ast.StarExpr:
		gen, newExpr := ac.convertExpr(v.X)
		v.X = newExpr
		return gen, v
	case *ast.UnaryExpr:
		gen, newExpr := ac.convertExpr(v.X)
		v.X = newExpr
		return gen, v
	case *ast.BinaryExpr:
		var gens []ast.Stmt

		gen, newExpr := ac.convertExpr(v.X)
		gens = append(gens, gen...)
		v.X = newExpr

		gen, newExpr = ac.convertExpr(v.Y)
		gens = append(gens, gen...)
		v.Y = newExpr

		return gens, v
	case *ast.KeyValueExpr:
		gen, newExpr := ac.convertExpr(v.Value)
		v.Value = newExpr
		return gen, v
	default:
		return nil, v
	}
}

func (ac astContext) convertCheck(check ast.Expr) ([]ast.Stmt, exprList) {
	//fmt.Printf("%#v\n", check)

	pos := ac.nodePosDst(check) - ac.filePos + 1
	delete(ac.checkMap, pos)
	gen, expr := ac.convertExpr(check)

	var resultTypes []string
	exprCall, ok := expr.(*ast.CallExpr)
	if ok {
		var err error
		resultTypes, err = resultTypeNames(ac.astInfo, exprCall)
		if err != nil {
			panic(err)
		}
		if len(resultTypes) == 0 {
			panic(errors.New("check on func with no return values"))
		}
	} else {
		resultTypes = []string{"error"}
	}

	if resultTypes[len(resultTypes)-1] != "error" {
		panic(errors.New("last type of check expression must be error"))
	}

	varNames := make([]string, len(resultTypes))
	for i, rt := range resultTypes {
		curr := ac.varTypes[rt]
		ac.varTypes[rt]++
		varNames[i] = varPrefix + rt + strconv.Itoa(curr)
	}

	// need separate nodes to keep AST valid, especially if using github.com/dave/dst
	varErr := &ast.Ident{Name: varNames[len(varNames)-1]}
	varList := make([]ast.Expr, len(varNames))  // for generated code
	varList2 := make([]ast.Expr, len(varNames)) // for returned exprList
	for i, vn := range varNames {
		varList[i] = &ast.Ident{Name: vn}
		varList2[i] = &ast.Ident{Name: vn}
	}

	gen = append(gen, []ast.Stmt{
		&ast.AssignStmt{
			Lhs: varList,
			Tok: token.DEFINE,
			Rhs: []ast.Expr{expr},
		},
		&ast.IfStmt{
			Cond: &ast.BinaryExpr{
				X:  varErr,
				Op: token.NEQ,
				Y:  &ast.Ident{Name: "nil"},
			},
			Body: ac.evalHandleChain(varNames[len(varList)-1]),
		},
	}...)

	fmt.Println("WHAT")
	// leave out error
	return gen, exprList(varList2[0 : len(varList2)-1])
}

func (ac astContext) toHandleBlock(stmt ast.Stmt) (*ast.BlockStmt, bool) {
	if stmt == nil {
		return nil, false
	}
	pos := ac.nodePosDst(stmt) - ac.filePos + 1
	if _, ok := ac.handleMap[pos]; ok {
		return stmt.(*ast.BlockStmt), true
	}
	return nil, false
}

func (ac astContext) isCheckExpr(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	pos := ac.nodePosDst(expr) - ac.filePos + 1
	str, err := ac.nodeStringDst(expr)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%d: %#v\n", pos, str)
	if !ac.checkMap[pos] {
		return false
	}
	//fmt.Printf("%#v\n", ac.checkMap)
	switch expr.(type) {
	case *ast.BinaryExpr, *ast.KeyValueExpr:
		return false
	}
	return true
}

func defaultHandleStmt(ft *ast.FuncType, ai astInfo) ast.Stmt {
	var ftrl []*ast.Field
	if ft.Results != nil {
		ftrl = ft.Results.List
	}
	if len(ftrl) == 0 {
		return panicWithErrStmt(handleErr)
	}

	last := ftrl[len(ftrl)-1]

	lastIdent, ok := last.Type.(*ast.Ident)
	if !ok || lastIdent.Name != "error" {
		return panicWithErrStmt(handleErr)
	}

	resultList := make([]ast.Expr, len(ft.Results.List))
	for i, field := range ft.Results.List {
		if i < len(resultList)-1 {
			resultList[i] = &ast.Ident{
				Name: zeroValueString(ai, field.Type),
			}
		} else {
			resultList[i] = &ast.Ident{
				Name: handleErr,
			}
		}
	}

	return &ast.ReturnStmt{
		Results: resultList,
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

func varDecl(name string, typeExpr ast.Expr) *ast.DeclStmt {
	return &ast.DeclStmt{
		Decl: &ast.GenDecl{
			Tok: token.VAR,
			Specs: []ast.Spec{
				&ast.ValueSpec{
					Names: []*ast.Ident{
						&ast.Ident{
							Name: name,
						},
					},
					Type: typeExpr,
				},
			},
		},
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

func resultTypeNames(ai astInfo, call *ast.CallExpr) ([]string, error) {
	var ident *ast.Ident

	switch v := call.Fun.(type) {
	case *ast.FuncLit:
		names := make([]string, len(v.Type.Results.List))
		for i, field := range v.Type.Results.List {
			s, err := ai.nodeStringDst(field.Type)
			if err != nil {
				return nil, err
			}
			names[i] = s
		}
		return names, nil
	case *ast.Ident:
		ident = v
	case *ast.SelectorExpr:
		ident = v.Sel
	default:
		return nil, errors.New("error: CallExpr.Fun not Ident|SelectorExpr|FuncLit")
	}

	obj, ok := ai.infoUsesDst(ident)
	if !ok {
		return nil, errors.New("error: object not found")
	}
	sig, ok := obj.Type().(*types.Signature)
	if !ok {
		return nil, errors.New("error: object not a function")
	}

	l := sig.Results().Len()
	names := make([]string, l)
	for i := 0; i < l; i++ {
		names[i] = sig.Results().At(i).Type().String()
	}
	return names, nil
}

func zeroValueString(ai astInfo, typeExpr ast.Expr) string {
	t := ai.infoTypeOfDst(typeExpr)
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
