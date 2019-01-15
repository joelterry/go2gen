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

type go2Package struct {
	name string

	fset *token.FileSet

	// Not sure how to preserve an intermediate
	// token.FileSet for repeated use, so for now
	// I'm storing the .go files as *dst.File,
	// even though I'm not modifying them at all.
	goFiles []*ast.File

	go2Files []*go2File
}

func parsePkg(dirPath string) (*go2Package, error) {
	pkg, err := build.ImportDir(dirPath, 0)
	if err != nil {
		return nil, err
	}
	dir, err := os.Open(dirPath)
	if err != nil {
		return nil, err
	}
	files, err := dir.Readdirnames(0)
	if err != nil {
		return nil, err
	}

	isGo2 := make(map[string]bool)
	for _, file := range files {
		ext := path.Ext(file)
		name := file[0 : len(file)-len(ext)]
		if ext == extension {
			isGo2[name] = true
		}
	}

	fset := token.NewFileSet()
	var goFiles []*ast.File
	var go2Files []*go2File

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
				return nil, err
			}
			goFiles = append(goFiles, f)
			continue
		}

		b, err := ioutil.ReadFile(fullPath)
		if err != nil {
			return nil, err
		}

		src, cm, hm, err := process(string(b))
		if err != nil {
			return nil, err
		}

		f, err := parser.ParseFile(fset, "", src, 0)
		if err != nil {
			return nil, err
		}
		original, err := parser.ParseFile(token.NewFileSet(), "", src, parser.ParseComments)
		if err != nil {
			return nil, err
		}

		/*
			fmt.Printf("%v\n", f)
			fmt.Printf("%v\n", original)
		*/

		go2Files = append(go2Files, &go2File{
			name:      name,
			fset:      fset,
			f:         f,
			checkMap:  cm,
			handleMap: hm,
			original:  original,
		})
	}

	return &go2Package{
		name:     pkg.Name,
		fset:     fset,
		goFiles:  goFiles,
		go2Files: go2Files,
	}, nil
}

func (p *go2Package) checkTypes() (*types.Info, error) {
	var files []*ast.File
	files = append(files, p.goFiles...)
	for _, gf := range p.go2Files {
		files = append(files, gf.f)
	}
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
	cfg.Check(p.name, p.fset, files, info)
	return info, nil
}

func parseString(s string) (ast.Node, error) {
	return parser.ParseFile(token.NewFileSet(), "", s, 0)
}
