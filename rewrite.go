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

	// https://golang.org/pkg/go/scanner/#Scanner.Scan
	var sc scanner.Scanner
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	sc.Init(file, []byte(src), nil, 0)

	var sb strings.Builder

	rw := &rewriter{
		sc: &sc,
		sb: &sb,
	}

	return rw.rewrite()
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

type rewriter struct {
	sc      *scanner.Scanner
	sb      *strings.Builder
	err     error
	nextTok token.Token
	nextLit string
}

func (rw *rewriter) rewrite() (string, error) {
	rw.stateInit()
	if rw.err != nil {
		return "", rw.err
	}
	return rw.sb.String(), nil
}

func (rw *rewriter) scan() (token.Token, string) {
	if rw.nextTok != token.ILLEGAL {
		tok, lit := rw.nextTok, rw.nextLit
		rw.nextTok = token.ILLEGAL
		rw.nextLit = ""
		return tok, lit
	}
	_, tok, lit := rw.sc.Scan()
	return tok, lit
}

func (rw *rewriter) write(tok token.Token, lit string) {
	switch {
	case tok.IsOperator() || tok.IsKeyword():
		rw.sb.WriteString(tok.String())
	default:
		rw.sb.WriteString(lit)
	}
	rw.sb.WriteString(" ")
}

func (rw *rewriter) stateInit() {
	defer func() {
		if r := recover(); r != nil {
			rw.err = r.(error)
		}
	}()
	for {
		tok, lit := rw.scan()
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

		rw.write(tok, lit)
	}
}

func (rw *rewriter) stateCheck() {
	rw.write(token.IDENT, checkFunc)
	rw.write(token.LPAREN, "(")

	dc := delimCounter{}

	for {
		tok, lit := rw.scan()
		if tok == token.EOF {
			panic(errors.New("EOF in the middle of check"))
		}

		if lit == "check" {
			rw.stateCheck()
			continue
		}

		if dc.isZero() && exprEnd[tok] {
			rw.write(token.RPAREN, ")")
			rw.nextTok = tok
			rw.nextLit = lit
			break
		}

		dc.count(tok)

		if dc.isNegative() {
			panic(errors.New("mismatched delimiters"))
		}

		rw.write(tok, lit)
	}
}

func (rw *rewriter) stateHandle() {
	tok, lit := rw.scan()
	if tok != token.IDENT {
		panic(errors.New("expected identifier after handle"))
	}

	errName := lit

	tok, _ = rw.scan()
	if tok != token.LBRACE {
		panic(errors.New("expected left brace after 'handle IDENT'"))
	}

	rw.write(token.IF, "if")
	rw.write(token.IDENT, handleBool)
	rw.write(token.LBRACE, "{")

	dc := delimCounter{}
	dc.count(token.LBRACE)

	for {
		tok, lit := rw.scan()
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
			rw.write(token.IDENT, handleErr)
			continue
		}

		dc.count(tok)

		if dc.isNegative() {
			panic(errors.New("mismatched delimiters"))
		}

		rw.write(tok, lit)

		if dc.isZero() && tok.IsOperator() {
			break
		}
	}
}
