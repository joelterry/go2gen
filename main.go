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
	varPrefix          = "_go2var"
	errorPrefix        = "_go2err"
	handleResultPrefix = "_go2r"
	extension          = ".go2"
)

func main() {
	dir := "foo"

	fset := token.NewFileSet()
	fm, cm, err := parseDir(dir, fset)
	if err != nil {
		log.Fatal(err)
	}

	cv := &converter{
		cm: cm,
	}

	for name, f := range fm {
		cv.convertFile(f)

		if cv.err != nil {
			log.Fatal("convert err: ", cv.err)
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
