package main

import (
	"fmt"
	"testing"
)

type ie struct {
	int
	error
}

type se struct {
	string
	error
}

func TestRewriteValid(t *testing.T) {

	inputs := []string{
		"check nil",
		"check add(1, 2)",
		"check add(check add(1, 1), check add(1, 1))",
		"handle err { print(err) \n\t os.Exit(1) \n }",
	}
	outputs := []string{
		"_go2check(nil)",
		"_go2check(add(1, 2))",
		"_go2check(add(_go2check(add(1, 1)), _go2check(add(1, 1))))",
		"if _go2handle { print(_go2handleErr) \n\t os.Exit(1) \n }",
	}

	for i, input := range inputs {
		output := outputs[i]

		result, err := RewriteChecksAndHandles(input)
		if err != nil {
			fmt.Println("err: ", err)
			t.Fail()
		} else if result != output {
			fmt.Printf("fail: expected %s, but got %s\n", output, result)
			t.Fail()
		} else {
			fmt.Println("pass: ", result)
		}
	}
}
