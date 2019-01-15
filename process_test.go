package main

import (
	"testing"

	"github.com/joelterry/fun"
)

// process test case
type ptc struct {
	in  string
	out string
	checkMap
	handleMap
	error
}

func TestProcess(t *testing.T) {
	fun.Test(t, process).
		In("check nil").
		Out(
			"nil", checkMap{1: true}, handleMap{},
		).
		In("check add(1, 2)").
		Out(
			"add(1, 2)", checkMap{1: true}, handleMap{},
		).
		In("check add(check add(1, 1), check add(1, 1))").
		Out(
			"add(add(1, 1), add(1, 1))",
			checkMap{1: true, 5: true, 16: true},
			handleMap{},
		).
		In("x := check add(check add(1, 1), check add(1, 1))").
		Out(
			"x := add(add(1, 1), add(1, 1))",
			checkMap{6: true, 10: true, 21: true}, handleMap{}).
		In("handle errFoo { print(errFoo) \n\t os.Exit(1) \n }").
		Out(
			"{ print(errFoo) \n\t os.Exit(1) \n }",
			checkMap{},
			handleMap{1: "errFoo"},
		).
		In("check atoi(a) + check atoi(b) + check atoi(c)").
		Out(
			"atoi(a) + atoi(b) + atoi(c)",
			checkMap{1: true, 11: true, 21: true},
			handleMap{},
		).
		In(
			"handle ()").
		Err()
}
