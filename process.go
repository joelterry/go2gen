package main

import (
	"errors"
	"fmt"
	"go/scanner"
	"go/token"
	"io/ioutil"
	"os"
)

type checkMap map[token.Pos]bool

// Pos -> error variable name
type handleMap map[token.Pos]string

func process(src string) (string, checkMap, handleMap, error) {
	// https://golang.org/pkg/go/scanner/#Scanner.Scan
	var sc scanner.Scanner
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	sc.Init(file, []byte(src), nil, 0)

	var cs cuts

	cm := make(checkMap)
	hm := make(handleMap)

	var offset token.Pos

	for {
		pos, tok, lit := sc.Scan()
		if tok == token.EOF {
			break
		}
		switch lit {
		case "check":
			nextPos, nextTok, _ := sc.Scan()
			if nextTok == token.EOF {
				return "", nil, nil, errors.New("process error: unexpected EOF after check")
			}
			diff := nextPos - pos
			offset += diff
			cm[nextPos-offset] = true
			cs = append(cs, cut{start: int(pos) - 1, end: int(nextPos) - 1})
			//edits = append(edits, edit{index: int(pos) - 1, remove: int(diff)})
		case "handle":
			_, errTok, errLit := sc.Scan()
			if !errTok.IsLiteral() {
				return "", nil, nil, errors.New("process error: token after handle isn't literal")
			}
			nextPos, nextTok, _ := sc.Scan()
			if nextTok == token.EOF {
				return "", nil, nil, errors.New("process error: unexpected EOF after handle")
			}
			diff := nextPos - pos
			offset += diff
			hm[nextPos-offset] = errLit
			cs = append(cs, cut{start: int(pos) - 1, end: int(nextPos) - 1})
			//edits = append(edits, edit{index: int(pos) - 1, remove: int(diff)})
		}
	}

	s, err := cs.Apply(src)
	if err != nil {
		return "", nil, nil, err
	}
	return s, cm, hm, nil
}

// for debugging
func ProcessFile(input string, output string) {
	b, err := ioutil.ReadFile(input)
	if err != nil {
		panic(err)
	}
	s, cm, hm, err := process(string(b))
	if err != nil {
		panic(err)
	}
	err = ioutil.WriteFile(output, []byte(s), 0666)
	if err != nil {
		panic(err)
	}
	_, err = os.Stdout.Write([]byte(fmt.Sprintf("callMap: \n%#v\n\nhandleMap:\n%#v\n\n", cm, hm)))
	if err != nil {
		panic(err)
	}
}
