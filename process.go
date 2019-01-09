package main

import (
	"errors"
	"go/scanner"
	"go/token"
	"strings"
)

type checkMap map[token.Pos]bool

// Pos -> error variable name
type handleMap map[token.Pos]string

type cut struct {
	index  int
	length int
}

func process(src string) (string, checkMap, handleMap, error) {
	// https://golang.org/pkg/go/scanner/#Scanner.Scan
	var sc scanner.Scanner
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	sc.Init(file, []byte(src), nil, 0)

	var cuts []cut

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
			cuts = append(cuts, cut{index: int(pos) - 1, length: int(diff)})
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
			cuts = append(cuts, cut{index: int(pos) - 1, length: int(diff)})
		}
	}

	var sb strings.Builder
	i := 0
	for _, c := range cuts {
		_, err := sb.WriteString(src[i:c.index])
		if err != nil {
			return "", nil, nil, err
		}
		i = c.index + c.length
	}
	_, err := sb.WriteString(src[i:])
	if err != nil {
		return "", nil, nil, err
	}

	return sb.String(), cm, hm, nil
}
