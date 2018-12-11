package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/format"
	"go/parser"
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

type converter struct {
	numVals int
	numErrs int
}

func (cv *converter) convertAST(node ast.Node, cm callMap) error {

	// my guess for rules in passing this up:
	//   if parent is expr, continue passing up
	//   if parent is stmt, insert checks behind, and clear them
	var checks []ast.Node

	var applyErr error
	astutil.Apply(node, nil, func(c *astutil.Cursor) bool {
		if _, ok := c.Node().(ast.Stmt); ok {
			for _, checkNode := range checks {
				c.InsertBefore(checkNode)
			}
			// TODO: if checks exist, replace right side with
			// respective variables... store them on parent with map?
			//
			// ALSO need to do same with expr parents...
			checks = nil
		}

		call, ok := c.Node().(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok {
			return true
		}
		if ident.Name != checkFunc {
			return true
		}
		if len(call.Args) != 1 {
			applyErr = errors.New("invalid check call")
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

		var vals []string
		for i := 0; i < numResults-1; i++ {
			vals = append(vals, valuePrefix+strconv.Itoa(cv.numVals))
			cv.numVals++
		}
		vals = append(vals, errorPrefix+strconv.Itoa(cv.numErrs))
		cv.numErrs++

		checks = append(checks, checkToNodes(expr, vals)...)

		c.Replace(expr)

		return true
	})
	return applyErr
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
				Y:  errIdent,
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

func parseDir(dirPath string, fset *token.FileSet) (map[string]*ast.File, map[*ast.Ident]types.Object, error) {
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
		Error: func(err error) { log.Println(err) },
	}
	info := &types.Info{
		Uses: make(callMap),
	}
	cfg.Check(pkg.Name, fset, fs, info)

	return fm, info.Uses, nil
}

func indexExprEnd(s string) (int, error) {
	var parens, braces, brackets int
	for i, r := range s {
		switch r {
		case '(':
			parens++
		case '{':
			braces++
		case '[':
			brackets++
		case ')':
			parens--
		case '}':
			braces--
		case ']':
			brackets--
		case '\n', ';':
			if parens+braces+brackets == 0 {
				return i, nil
			}
		}
		if parens < 0 || braces < 0 || brackets < 0 {
			return 0, fmt.Errorf("invalid syntax:\n%s", s[:i+1])
		}
	}
	if parens+braces+brackets == 0 {
		return len(s), nil
	}
	return 0, fmt.Errorf("expression not found")
}

func consume(s, chars string) string {
	for i := range s {
		if !strings.ContainsAny(s[i:i+1], chars) {
			return s[i:]
		}
	}
	return ""
}

func replaceChecks(src string) (string, error) {
	var sb strings.Builder

	checkI := strings.Index(src, "check ")
	for ; checkI >= 0; checkI = strings.Index(src, "check ") {
		sb.WriteString(src[:checkI])

		src = src[checkI+6:]

		exprEnd, err := indexExprEnd(src)
		if err != nil {
			return "", err
		}

		sb.WriteString(checkFunc + "(")

		arg, err := replaceChecks(src[:exprEnd])
		if err != nil {
			return "", err
		}

		sb.WriteString(arg)
		sb.WriteString(")")

		src = src[exprEnd:]
	}

	sb.WriteString(src)

	return sb.String(), nil
}

func removeChecks(src string) (string, error) {
	var sb strings.Builder

	checkI := strings.Index(src, "check ")
	for ; checkI >= 0; checkI = strings.Index(src, "check ") {
		sb.WriteString(src[:checkI])

		src = src[checkI+6:]

		exprEnd, err := indexExprEnd(src)
		if err != nil {
			return "", err
		}

		src = consume(src[exprEnd:], ";\n\t ")
	}

	sb.WriteString(src)

	return sb.String(), nil
}
