package main

import (
	"errors"
	"go/ast"
	"go/types"
)

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
		return nil
	}

	last := ftrl[len(ftrl)-1]
	lastIdent, ok := last.Type.(*ast.Ident)
	if !ok || lastIdent.Name != "error" {
		return nil
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

func isDefined(t types.Type) bool {
	if t == nil {
		return false
	}
	switch v := t.(type) {
	case *types.Basic:
		return v.Kind() != types.Invalid
	case *types.Tuple:
		l := v.Len()
		for i := 0; i < l; i++ {
			item := v.At(i)
			if !isDefined(item.Type()) {
				return false
			}
		}
		return true
	default:
		return true
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
