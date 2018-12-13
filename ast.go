package main

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strconv"
)

func parseString(s string) (ast.Node, error) {
	return parser.ParseFile(token.NewFileSet(), "", s, 0)
}

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

// callMap has type information gathered in the parsing phase via types.Info.Uses.
type callMap map[*ast.Ident]types.Object

func (cm callMap) resultCount(call *ast.CallExpr) (int, error) {
	ident, ok := call.Fun.(*ast.Ident)
	if !ok {
		lit, ok := call.Fun.(*ast.FuncLit)
		if !ok {
			return 0, errors.New("callMap: CallExpr.Fun not Ident or FuncLit")
		}
		return len(lit.Type.Results.List), nil
	}
	obj, ok := cm[ident]
	if !ok {
		return 0, errors.New("callMap: object not found")
	}
	sig, ok := obj.Type().(*types.Signature)
	if !ok {
		return 0, errors.New("callMap: object not function")
	}
	return sig.Results().Len(), nil
}

type converter struct {
	cm      callMap
	numVars int
}

// converts top-level function declarations and literals
func (cv *converter) convertFile(f *ast.File) {
	for _, decl := range f.Decls {
		switch v := decl.(type) {
		case *ast.FuncDecl:
			cv.convertFunc(v.Type, v.Body)
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
					cv.convertFunc(funcLit.Type, funcLit.Body)
				}
			}
		}
	}
}

func (cv *converter) convertFunc(ft *ast.FuncType, body *ast.BlockStmt) {
	top, bottom := handleTopAndBottom(ft)
	var hc handlerChain
	for _, stmt := range top {
		hc.top = append(hc.top, stmt)
	}
	hc.bottom = bottom
	cv.convertBlock(hc, body)
}

func (cv *converter) convertBlock(hc handlerChain, block *ast.BlockStmt) {
	var newList []ast.Stmt
	for _, stmt := range block.List {
		handleBlock, ok := toHandleBlock(stmt)
		if ok {
			hc = hc.extend(handleBlock)
			continue
		}
		generated := cv.convertStmt(hc, stmt)
		newList = append(newList, generated...)
		newList = append(newList, stmt)
	}
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
func (cv *converter) convertStmt(hc handlerChain, stmt ast.Stmt) []ast.Stmt {
	switch v := stmt.(type) {
	// TODO: fill these out
	case *ast.DeclStmt:
	case *ast.ExprStmt:
	case *ast.SendStmt:
	case *ast.AssignStmt:
	case *ast.ReturnStmt:

	case *ast.BlockStmt:
		cv.convertBlock(hc, v)

	case *ast.IfStmt:
		cv.convertBlock(hc, v.Body)
		cv.convertStmt(hc, v.Else)
	case *ast.CaseClause:
		for _, s := range v.Body {
			cv.convertStmt(hc, s)
		}
	case *ast.SwitchStmt:
		cv.convertBlock(hc, v.Body)
	case *ast.TypeSwitchStmt:
		cv.convertBlock(hc, v.Body)
	case *ast.CommClause:
		for _, s := range v.Body {
			cv.convertStmt(hc, s)
		}
	case *ast.SelectStmt:
		cv.convertBlock(hc, v.Body)
	case *ast.ForStmt:
		cv.convertBlock(hc, v.Body)
	case *ast.RangeStmt:
		cv.convertBlock(hc, v.Body)
	default:
		return nil
	}
	return nil
}

// returns generated code and altered expression list
//
// this is the only place where an exprList returned from convertCheck
// isn't automatically "cast" to an Expr
func (cv *converter) convertExprs(hc handlerChain, exprs []ast.Expr) ([]ast.Stmt, []ast.Expr) {
	var generated []ast.Stmt
	var newExprs []ast.Expr

	for _, expr := range exprs {
		if checkCall, ok := toCheckCall(expr); ok {
			stmts, newExprList := cv.convertCheck(hc, checkCall)
			if len(newExprList) > 1 {
				if len(exprs) > 1 {
					panic("error: multiple values in context where only one is allowed")
				}
				return stmts, newExprList
			}
			generated = append(generated, stmts...)
			newExprs = append(newExprs, newExprList...)
			continue
		}
		stmts, newExpr := cv.convertExpr(hc, expr)
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
func (cv *converter) convertExpr(hc handlerChain, expr ast.Expr) ([]ast.Stmt, ast.Expr) {
	// function literal is special case: start new conversion
	if funcLit, ok := expr.(*ast.FuncLit); ok {
		cv.convertFunc(funcLit.Type, funcLit.Body)
		return nil, expr
	}

	if checkCall, ok := toCheckCall(expr); ok {
		gen, newExprList := cv.convertCheck(hc, checkCall)
		return gen, newExprList.Expr()
	}

	// https://golang.org/src/go/ast/ast.go#L227
	switch v := expr.(type) {
	case *ast.CompositeLit:
		gen, newExprs := cv.convertExprs(hc, v.Elts)
		v.Elts = newExprs
		return gen, v
	case *ast.ParenExpr:
		if v.X == nil {
			return nil, v
		}
		gen, newExpr := cv.convertExpr(hc, v.X)
		v.X = newExpr
		return gen, v
	case *ast.SelectorExpr:
		if v.X == nil {
			return nil, v
		}
		gen, newExpr := cv.convertExpr(hc, v.X)
		v.X = newExpr
		return gen, v
	case *ast.IndexExpr:
		var gens []ast.Stmt
		if v.X != nil {
			gen, newExpr := cv.convertExpr(hc, v.X)
			gens = append(gens, gen...)
			v.X = newExpr
		}
		if v.Index != nil {
			gen, newExpr := cv.convertExpr(hc, v.Index)
			gens = append(gens, gen...)
			v.Index = newExpr
		}
		return gens, v
	case *ast.SliceExpr:
		var gens []ast.Stmt
		if v.Low != nil {
			gen, newExpr := cv.convertExpr(hc, v.Low)
			gens = append(gens, gen...)
			v.Low = newExpr
		}
		if v.High != nil {
			gen, newExpr := cv.convertExpr(hc, v.High)
			gens = append(gens, gen...)
			v.High = newExpr
		}
		if v.Max != nil {
			gen, newExpr := cv.convertExpr(hc, v.Max)
			gens = append(gens, gen...)
			v.Max = newExpr
		}
		return gens, v
	case *ast.TypeAssertExpr:
		if v.X == nil {
			return nil, v
		}
		gen, newExpr := cv.convertExpr(hc, v.X)
		v.X = newExpr
		return gen, v
	case *ast.CallExpr:
		var gens []ast.Stmt
		if v.Fun != nil {
			gen, newExpr := cv.convertExpr(hc, v.Fun)
			gens = append(gens, gen...)
			v.Fun = newExpr
		}
		gen, newExprs := cv.convertExprs(hc, v.Args)
		gens = append(gens, gen...)
		v.Args = newExprs
		return gen, v
	case *ast.StarExpr:
		if v.X == nil {
			return nil, v
		}
		gen, newExpr := cv.convertExpr(hc, v.X)
		v.X = newExpr
		return gen, v
	case *ast.UnaryExpr:
		if v.X == nil {
			return nil, v
		}
		gen, newExpr := cv.convertExpr(hc, v.X)
		v.X = newExpr
		return gen, v
	case *ast.BinaryExpr:
		var gens []ast.Stmt
		if v.X != nil {
			gen, newExpr := cv.convertExpr(hc, v.X)
			gens = append(gens, gen...)
			v.X = newExpr
		}
		if v.Y != nil {
			gen, newExpr := cv.convertExpr(hc, v.Y)
			gens = append(gens, gen...)
			v.Y = newExpr
		}
		return gens, v
	case *ast.KeyValueExpr:
		if v.Value == nil {
			return nil, nil
		}
		gen, newExpr := cv.convertExpr(hc, v.Value)
		v.Value = newExpr
		return gen, v
	default:
		return nil, nil
	}
}

func (cv *converter) convertCheck(hc handlerChain, call *ast.CallExpr) ([]ast.Stmt, exprList) {
	// call should be confirmed as check already
	expr := call.Args[0]

	// get the number of results passed to check
	numResults := 1
	exprCall, ok := expr.(*ast.CallExpr)
	if ok {
		var err error
		numResults, err = cv.cm.resultCount(exprCall)
		if err != nil {
			panic(err)
		}
	}
	if numResults == 0 {
		panic(errors.New("check on func with no return values"))
	}

	// not sure if I need separate nodes to keep AST valid...
	varList := make([]ast.Expr, numResults)  // for generated code
	varList2 := make([]ast.Expr, numResults) // for returned exprList

	for i := 0; i < numResults; i++ {
		varName := varPrefix + strconv.Itoa(cv.numVars)
		cv.numVars++

		varList[i] = &ast.Ident{Name: varName}
		varList2[i] = &ast.Ident{Name: varName}
	}

	gen := []ast.Stmt{
		&ast.AssignStmt{
			Lhs: varList,
			Tok: token.DEFINE,
			Rhs: []ast.Expr{expr},
		},
		&ast.IfStmt{
			Cond: &ast.BinaryExpr{
				X:  varList[len(varList)-1],
				Op: token.NEQ,
				Y:  &ast.Ident{Name: "nil"},
			},
			Body: hc.eval(),
		},
	}

	return gen, exprList(varList2)
}

type handlerChain struct {
	top    []ast.Stmt
	bottom ast.Stmt
	stack  [][]ast.Stmt
}

func (hc handlerChain) extend(block *ast.BlockStmt) handlerChain {
	return handlerChain{
		top:    hc.top,
		bottom: hc.bottom,
		stack:  append(hc.stack, block.List),
	}
}

func (hc handlerChain) eval() *ast.BlockStmt {
	b := &ast.BlockStmt{}
	b.List = append(b.List, hc.top...)
	for i := len(hc.stack) - 1; i >= 0; i-- {
		b.List = append(b.List, hc.stack[i]...)
	}
	b.List = append(b.List, hc.bottom)
	return b
}

// Returns the top and bottom of the handler chain for function with type FT.
// The top var declarations are to simplify returning zero values.
func handleTopAndBottom(ft *ast.FuncType) ([]*ast.DeclStmt, *ast.ReturnStmt) {
	var decls []*ast.DeclStmt
	ret := &ast.ReturnStmt{}
	for i, field := range ft.Results.List {
		name := handleResultPrefix + strconv.Itoa(i)
		decls = append(decls, &ast.DeclStmt{
			Decl: &ast.GenDecl{
				Tok: token.VAR,
				Specs: []ast.Spec{
					&ast.ValueSpec{
						Names: []*ast.Ident{
							&ast.Ident{
								Name: name,
							},
						},
						Type: field.Type,
					},
				},
			},
		})
		ret.Results = append(ret.Results, &ast.Ident{
			Name: name,
		})
	}
	return decls, ret
}

func toHandleBlock(node ast.Node) (*ast.BlockStmt, bool) {
	if node == nil {
		return nil, false
	}
	ifstmt, ok := node.(*ast.IfStmt)
	if !ok {
		return nil, false
	}
	ident, ok := ifstmt.Cond.(*ast.Ident)
	if !ok {
		return nil, false
	}
	if ident.Name != handleBool {
		return nil, false
	}
	return ifstmt.Body, true
}

func toCheckCall(node ast.Node) (*ast.CallExpr, bool) {
	if node == nil {
		return nil, false
	}
	call, ok := node.(*ast.CallExpr)
	if !ok {
		return nil, false
	}
	ident, ok := call.Fun.(*ast.Ident)
	if !ok {
		return nil, false
	}
	if ident.Name != checkFunc {
		return nil, false
	}
	if len(call.Args) != 1 {
		panic(errors.New("invalid check call: must only have one arg"))
	}
	return call, true
}
