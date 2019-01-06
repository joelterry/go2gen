package main

import (
	"go/format"
	"go/token"
	"log"
	"os"
	"path"
)

const (
	checkFunc          = "_go2check"
	handleBool         = "_go2handle"
	handleErr          = "_go2handleErr"
	valuePrefix        = "_go2val"
	varPrefix          = "_go2_"
	errorPrefix        = "_go2err"
	handleResultPrefix = "_go2r"
	extension          = ".go2"
)

func main() {
	dir, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	fset := token.NewFileSet()
	fm, info, err := parseDir(dir, fset)
	if err != nil {
		log.Fatal(err)
	}

	ac := astContext{
		fset: fset,
		info: info,
	}

	for name, f := range fm {
		err := ac.convertFile(f)

		if err != nil {
			log.Fatal("convert err: ", err)
		}
		w, err := os.Create(path.Join(dir, name) + ".go")
		if err != nil {
			log.Fatal("file err: ", err)
		}
		err = format.Node(w, fset, f)
		if err != nil {
			panic(err)
		}
	}
}
