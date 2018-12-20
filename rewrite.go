package main

import (
	"errors"
	"go/scanner"
	"go/token"
	"strings"
)

// RewriteChecksAndHandles replaces instances of the check
// and handle keywords with Go code that will be understood
// by the parser.
//
// check <expr> => _go2check(<expr>)
// handle <err> { <body> } => if _go2handle { <body (all instances of <err> replaced with _go2handleErr) > }
func RewriteChecksAndHandles(src string) (string, error) {
	return newRewriter(src).rewrite()
}

// https://golang.org/src/go/parser/parser.go#L502
var exprEnd = map[token.Token]bool{
	token.COMMA:     true,
	token.COLON:     true,
	token.SEMICOLON: true,
	token.RPAREN:    true,
	token.RBRACK:    true,
	token.RBRACE:    true,
}

type delimCounter [3]int

func (dc *delimCounter) isZero() bool {
	return *dc == [3]int{}
}

func (dc *delimCounter) isNegative() bool {
	return dc[0] < 0 || dc[1] < 0 || dc[2] < 0
}

func (dc *delimCounter) count(tok token.Token) {
	switch tok {
	case token.LPAREN:
		dc[0]++
	case token.LBRACK:
		dc[1]++
	case token.LBRACE:
		dc[2]++
	case token.RPAREN:
		dc[0]--
	case token.RBRACK:
		dc[1]--
	case token.RBRACE:
		dc[2]--
	}
}

type stringChange struct {
	value string
	start int
	end   int
}

type rewriter struct {
	src  string
	fset *token.FileSet
	sc   *scanner.Scanner

	err error

	index int
	tok   token.Token
	lit   string

	changes []stringChange

	rewind bool
}

func newRewriter(src string) *rewriter {
	// https://golang.org/pkg/go/scanner/#Scanner.Scan
	var sc scanner.Scanner
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	sc.Init(file, []byte(src), nil, 0)

	return &rewriter{
		src:  src,
		fset: fset,
		sc:   &sc,
	}
}

func (rw *rewriter) rewrite() (string, error) {
	rw.stateInit()
	if rw.err != nil {
		return "", rw.err
	}

	var sb strings.Builder

	si := 0
	for _, change := range rw.changes {
		_, err := sb.WriteString(rw.src[si:change.start])
		if err != nil {
			return "", err
		}
		_, err = sb.WriteString(change.value)
		if err != nil {
			return "", err
		}
		si = change.end
	}
	sb.WriteString(rw.src[si:])

	return sb.String(), nil
}

func (rw *rewriter) scan() (int, token.Token, string) {
	if rw.rewind {
		rw.rewind = false
		return rw.index, rw.tok, rw.lit
	}
	pos, tok, lit := rw.sc.Scan()
	rw.index = rw.fset.Position(pos).Offset
	rw.tok, rw.lit = tok, lit
	return rw.index, rw.tok, rw.lit
}

func (rw *rewriter) stateInit() {
	defer func() {
		if r := recover(); r != nil {
			rw.err = r.(error)
		}
	}()
	for {
		_, tok, lit := rw.scan()
		if tok == token.EOF {
			break
		}

		if lit == "check" {
			rw.stateCheck()
			continue
		}

		if lit == "handle" {
			rw.stateHandle()
			continue
		}

	}
}

func (rw *rewriter) stateCheck() {
	rw.changes = append(rw.changes, stringChange{
		value: checkFunc + "(",
		start: rw.index,
		end:   rw.index + len("check "),
	})

	dc := delimCounter{}

	for {
		_, tok, lit := rw.scan()
		if tok == token.EOF {
			panic(errors.New("EOF in the middle of check"))
		}

		if lit == "check" {
			rw.stateCheck()
			continue
		}

		if dc.isZero() && exprEnd[tok] {
			rw.rewind = true

			// start same as end since ")" is an insert
			rw.changes = append(rw.changes, stringChange{
				value: ")",
				start: rw.index,
				end:   rw.index,
			})

			break
		}

		dc.count(tok)

		if dc.isNegative() {
			panic(errors.New("mismatched delimiters"))
		}

	}
}

func (rw *rewriter) stateHandle() {
	handleI := rw.index

	_, tok, lit := rw.scan()
	if tok != token.IDENT {
		panic(errors.New("expected identifier after handle"))
	}

	errName := lit

	rw.changes = append(rw.changes, stringChange{
		value: "if " + handleBool,
		start: handleI,
		end:   rw.index + len(errName),
	})

	_, tok, _ = rw.scan()
	if tok != token.LBRACE {
		panic(errors.New("expected left brace after 'handle IDENT'"))
	}

	dc := delimCounter{}
	dc.count(token.LBRACE)

	for {
		_, tok, lit := rw.scan()
		if tok == token.EOF {
			panic(errors.New("EOF in the middle of handle"))
		}

		if lit == "check" {
			panic(errors.New("check not allowed inside handler"))
		}
		if lit == "handle" {
			panic(errors.New("handle not allowed inside handler"))
		}

		if lit == errName {
			rw.changes = append(rw.changes, stringChange{
				value: handleErr,
				start: rw.index,
				end:   rw.index + len(errName),
			})
			continue
		}

		dc.count(tok)

		if dc.isNegative() {
			panic(errors.New("mismatched delimiters"))
		}

		if dc.isZero() && tok.IsOperator() {
			break
		}
	}
}
