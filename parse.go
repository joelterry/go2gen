package main

import (
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io/ioutil"
	"log"
	"os"
	"path"
)

type processedFile struct {
	*ast.File
	checkMap
	handleMap
	name string
}

func parseString(s string) (ast.Node, error) {
	return parser.ParseFile(token.NewFileSet(), "", s, 0)
}

func parseDir(dirPath string, fset *token.FileSet) ([]processedFile, *types.Info, error) {
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

	var allFiles []*ast.File
	var go2Files []processedFile

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
			allFiles = append(allFiles, f)
			continue
		}

		b, err := ioutil.ReadFile(fullPath)
		if err != nil {
			return nil, nil, err
		}

		src, cm, hm, err := process(string(b))
		if err != nil {
			return nil, nil, err
		}

		f, err := parser.ParseFile(fset, "", src, parser.ParseComments)
		if err != nil {
			return nil, nil, err
		}

		allFiles = append(allFiles, f)
		go2Files = append(go2Files, processedFile{
			File:      f,
			checkMap:  cm,
			handleMap: hm,
			name:      name,
		})
	}

	// https://github.com/golang/example/tree/master/gotypes#identifier-resolution
	cfg := &types.Config{
		Error:    func(err error) { log.Println(err) },
		Importer: importer.Default(),
	}
	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Implicits:  make(map[ast.Node]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
		Scopes:     make(map[ast.Node]*types.Scope),
	}
	cfg.Check(pkg.Name, fset, allFiles, info)

	return go2Files, info, nil
}
