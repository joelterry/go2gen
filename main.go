package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/format"
	"go/importer"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"io/ioutil"
	"log"
	"os"
	"path"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
)

const (
	checkFunc   = "_go2check"
	valuePrefix = "_go2val"
	errorPrefix = "_go2err"
	extension   = ".go2"
)

func main() {
	dir := "foo"

	fset := token.NewFileSet()
	fm, cm, err := parseDir(dir, fset)
	if err != nil {
		log.Fatal(err)
	}

	cv := &converter{}

	for name, f := range fm {
		err := cv.convertAST(f, cm)
		if err != nil {
			log.Fatal(err)
		}
		w, err := os.Create(path.Join(dir, name) + ".go")
		if err != nil {
			log.Fatal(err)
		}
		err = format.Node(w, fset, f)
		if err != nil {
			panic(err)
		}
	}
}

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

type argMap map[ast.Node][][]string

func (am argMap) add(node ast.Node, vars []string) {
	if len(vars) == 0 {
		return
	}
	am[node] = append(am[node], vars)
}

func (am argMap) get(node ast.Node) ([]string, error) {
	parts := am[node]
	if len(parts) == 0 {
		return []string{}, nil
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	var all []string
	for _, part := range parts {
		if len(part) > 1 {
			return nil, errors.New("error: more than one multiple return")
		}
		all = append(all, part...)
	}
	return all, nil
}

type converter struct {
	numVals int
	numErrs int
}

func (cv *converter) convertAST(node ast.Node, cm callMap) error {

	codeStacks := make(map[ast.Stmt][][]ast.Node)
	am := make(argMap)
	var currStmt ast.Stmt
	var applyErr error

	preorder := func(c *astutil.Cursor) bool {
		if stmt, ok := c.Node().(ast.Stmt); ok {
			currStmt = stmt
			return true
		}

		call, ok := checkNode(c.Node())
		if !ok {
			return true
		}
		_, ok = checkNode(c.Parent())
		if ok {
			applyErr = errors.New("error: directly nested checks")
			return false
		}
		if len(call.Args) != 1 {
			applyErr = errors.New("invalid check")
			return false
		}

		expr := call.Args[0]

		numResults := 1
		innerCall, ok := expr.(*ast.CallExpr)
		if ok {
			numResults, applyErr = cm.resultCount(innerCall)
			if applyErr != nil {
				return false
			}
		}
		if numResults == 0 {
			applyErr = errors.New("check on func with no return values")
			return false
		}
		if numResults == 1 {
			if _, ok := c.Parent().(*ast.ExprStmt); !ok {
				applyErr = errors.New("check expression with single value must be an expression statement")
				return false
			}
		}

		var vals []string
		for i := 0; i < numResults-1; i++ {
			vals = append(vals, valuePrefix+strconv.Itoa(cv.numVals))
			cv.numVals++
		}
		vals = append(vals, errorPrefix+strconv.Itoa(cv.numErrs))
		cv.numErrs++

		// don't include error
		am.add(c.Parent(), vals[:len(vals)-1])

		// currStmt shouldn't be nil at this point
		codeStacks[currStmt] = append(codeStacks[currStmt], checkToNodes(expr, vals))

		c.Replace(expr)
		return true
	}

	postorder := func(c *astutil.Cursor) bool {
		node := c.Node()

		args, err := am.get(node)
		if err != nil {
			applyErr = err
			return false
		}
		if len(args) > 0 {
			exprs := make([]ast.Expr, len(args))
			for i, arg := range args {
				exprs[i] = &ast.Ident{Name: arg}
			}
			replaceArgs(node, exprs)
		}

		stmt, ok := node.(ast.Stmt)
		if !ok {
			return true
		}
		stack := codeStacks[stmt]
		for i := len(stack) - 1; i >= 0; i-- {
			nodes := stack[i]
			for _, node := range nodes {
				c.InsertBefore(node)
			}
		}
		return true
	}

	astutil.Apply(node, preorder, postorder)
	return applyErr
}

func replaceArgs(node ast.Node, args []ast.Expr) error {
	switch v := node.(type) {
	case *ast.CallExpr:
		v.Args = args
	case *ast.AssignStmt:
		v.Rhs = args
	default:
		return fmt.Errorf("node type not supported by replaceArgs: %v", v)
	}
	return nil
}

func checkNode(node ast.Node) (*ast.CallExpr, bool) {
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
	return call, true
}

func checkToNodes(expr ast.Expr, vals []string) []ast.Node {
	if len(vals) == 0 {
		panic("checkToNodes: vals must not be empty")
	}
	lsh := make([]ast.Expr, len(vals))
	for i, val := range vals {
		lsh[i] = &ast.Ident{
			Name: val,
		}
	}
	errIdent := lsh[len(lsh)-1]
	panicExpr := &ast.CallExpr{
		Fun: &ast.Ident{
			Name: "panic",
		},
		Args: []ast.Expr{
			errIdent,
		},
	}
	return []ast.Node{
		&ast.AssignStmt{
			Lhs: lsh,
			Tok: token.DEFINE,
			Rhs: []ast.Expr{expr},
		},
		&ast.IfStmt{
			Cond: &ast.BinaryExpr{
				X:  errIdent,
				Op: token.NEQ,
				Y:  &ast.Ident{Name: "nil"},
			},
			Body: &ast.BlockStmt{
				List: []ast.Stmt{
					&ast.ExprStmt{
						X: panicExpr,
					},
				},
			},
		},
	}
}

func parseDir(dirPath string, fset *token.FileSet) (map[string]*ast.File, callMap, error) {
	pkg, err := build.ImportDir(dirPath, 0)
	if err != nil {
		return nil, nil, err
	}

	dir, err := os.Open(dirPath)
	if err != nil {
		return nil, nil, err
	}
	files, err := dir.Readdirnames(0)
	if err != nil {
		return nil, nil, err
	}

	isGo2 := make(map[string]bool)
	for _, file := range files {
		ext := path.Ext(file)
		name := file[0 : len(file)-len(ext)]
		if ext == extension {
			isGo2[name] = true
		}
	}

	var fs []*ast.File
	fm := make(map[string]*ast.File)

	for _, file := range files {

		ext := path.Ext(file)
		name := file[0 : len(file)-len(ext)]
		if ext != ".go" && ext != extension {
			continue
		}
		// skip previously generated files
		if ext == ".go" && isGo2[name] {
			continue
		}

		fullPath := path.Join(dirPath, file)

		if ext == ".go" && !isGo2[name] {
			f, err := parser.ParseFile(fset, fullPath, nil, 0)
			if err != nil {
				return nil, nil, err
			}
			fs = append(fs, f)
			fm[name] = f
			continue
		}

		b, err := ioutil.ReadFile(fullPath)
		if err != nil {
			return nil, nil, err
		}
		src, err := replaceChecks(string(b))
		if err != nil {
			return nil, nil, err
		}
		f, err := parser.ParseFile(fset, "", src, 0)
		if err != nil {
			return nil, nil, err
		}
		fs = append(fs, f)
		fm[name] = f
	}

	cfg := &types.Config{
		Error:    func(err error) { log.Println(err) },
		Importer: importer.Default(),
	}
	info := &types.Info{
		Uses: make(callMap),
	}
	cfg.Check(pkg.Name, fset, fs, info)

	return fm, info.Uses, nil
}

func replaceChecks(src string) (string, error) {
	// https://golang.org/pkg/go/scanner/#Scanner.Scan
	var sc scanner.Scanner
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	sc.Init(file, []byte(src), nil, 0)

	var sb strings.Builder

	var delimiterStack [][3]int
	const (
		parens   = 0
		brackets = 1
		braces   = 2
	)
	di := map[token.Token]int{
		token.LPAREN: parens,
		token.RPAREN: parens,
		token.LBRACK: brackets,
		token.RBRACK: brackets,
		token.LBRACE: braces,
		token.RBRACE: braces,
	}

	for {
		_, tok, lit := sc.Scan()
		if tok == token.EOF {
			break
		}

		if lit == "check" {
			sb.WriteString(checkFunc + " ( ")
			delimiterStack = append(delimiterStack, [3]int{})
			continue
		}

		if len(delimiterStack) > 0 {
			top := len(delimiterStack) - 1

			switch tok {
			case token.LPAREN, token.LBRACK, token.LBRACE:
				delimiterStack[top][di[tok]]++
			case token.RPAREN, token.RBRACK, token.RBRACE:
				delimiterStack[top][di[tok]]--
				if delimiterStack[top] == [3]int{} {
					sb.WriteString(") ")
					delimiterStack = delimiterStack[:top]
				}
			default:
				if tok.IsOperator() && delimiterStack[top] == [3]int{} {
					sb.WriteString(") ")
					delimiterStack = delimiterStack[:top]
				}
			}
		}

		switch {
		case tok.IsOperator() || tok.IsKeyword():
			sb.WriteString(tok.String())
		default:
			sb.WriteString(lit)
		}
		sb.WriteString(" ")
	}

	if len(delimiterStack) > 0 {
		return "", errors.New("scan error")
	}

	return sb.String(), nil
}
