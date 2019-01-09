package main

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/printer"
	"go/token"
	"go/types"
	"io"

	"github.com/dave/dst"
	"github.com/dave/dst/decorator"
	"github.com/go-toolsmith/astcopy"
)

// The code in ast.go can ALMOST use packages go/ast and github.com/dave/dst interchangeably
// (with the latter aliased to "ast"). The instances where it can't have been isolated
// to these functions.

func copyBlockAst(block *ast.BlockStmt) *ast.BlockStmt {
	return astcopy.BlockStmt(block)
}

func copyBlockDst(block *dst.BlockStmt) *dst.BlockStmt {
	return dst.Clone(block).(*dst.BlockStmt)
}

func (ai astInfo) infoUsesAst(ident *ast.Ident) (types.Object, bool) {
	obj, ok := ai.info.Uses[ident]
	return obj, ok
}

func (ai astInfo) infoUsesDst(ident *dst.Ident) (types.Object, bool) {
	astIdent := ai.dec.Ast.Nodes[ident].(*ast.Ident)
	obj, ok := ai.info.Uses[astIdent]
	return obj, ok
}

func (ai astInfo) infoTypeOfAst(expr ast.Expr) types.Type {
	return ai.info.TypeOf(expr)
}

func (ai astInfo) infoTypeOfDst(expr dst.Expr) types.Type {
	astExpr := ai.dec.Ast.Nodes[expr].(ast.Expr)
	return ai.info.TypeOf(astExpr)
}

func (ai astInfo) nodeStringAst(node ast.Node) (string, error) {
	cfg := &printer.Config{
		Mode: printer.RawFormat,
	}
	var buf bytes.Buffer
	err := cfg.Fprint(&buf, ai.fset, node)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (ai astInfo) nodeStringDst(node dst.Node) (string, error) {
	astNode := ai.dec.Ast.Nodes[node]

	cfg := &printer.Config{
		Mode: printer.RawFormat,
	}
	var buf bytes.Buffer
	err := cfg.Fprint(&buf, ai.fset, astNode)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (ai astInfo) writeFileAst(w io.Writer, f *ast.File) error {
	return format.Node(w, ai.fset, f)
}

func (ai astInfo) writeFileDst(w io.Writer, f *dst.File) error {
	return decorator.Fprint(w, f)
}

func (ai astInfo) nodePosAst(node ast.Node) token.Pos {
	return node.Pos()
}

func (ai astInfo) nodePosDst(node dst.Node) token.Pos {
	return ai.dec.Ast.Nodes[node].Pos()
}
