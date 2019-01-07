package main

import (
	"go/token"
	"log"
	"os"
	"path"

	"github.com/dave/dst/decorator"
)

const (
	checkFunc        = "_go2check"
	handleBool       = "_go2handle"
	handleErr        = "_go2handleErr"
	varPrefix        = "_go2"
	extension        = ".go2"
	generatedComment = "// generated by go2gen; DO NOT EDIT"
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

	dec := decorator.NewDecorator(fset)
	ai := astInfo{
		fset: fset,
		info: info,
		dec:  dec,
	}
	ac := astContext{
		astInfo: ai,
	}

	for name, f := range fm {
		df, err := dec.DecorateFile(f)
		if err != nil {
			log.Fatal(err)
		}

		err = ac.convertFile(df)

		if err != nil {
			log.Fatal("convert err: ", err)
		}
		w, err := os.Create(path.Join(dir, name) + ".go")
		if err != nil {
			log.Fatal("file err: ", err)
		}
		_, err = w.WriteString(generatedComment + "\n\n")
		if err != nil {
			log.Fatal(err)
		}
		err = ai.writeFileDst(w, df)
		if err != nil {
			log.Fatal(err)
		}
	}
}
