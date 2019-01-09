package main

import (
	"fmt"
	"reflect"
	"testing"
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
	cases := []ptc{
		ptc{
			in:  "check nil",
			out: "nil",
			checkMap: checkMap{
				1: true,
			},
			handleMap: handleMap{},
		},
		ptc{
			in:  "check add(1, 2)",
			out: "add(1, 2)",
			checkMap: checkMap{
				1: true,
			},
			handleMap: handleMap{},
		},
		ptc{
			in:  "check add(check add(1, 1), check add(1, 1))",
			out: "add(add(1, 1), add(1, 1))",
			checkMap: checkMap{
				1:  true,
				5:  true,
				16: true,
			},
			handleMap: handleMap{},
		},
		ptc{
			in:       "handle errFoo { print(errFoo) \n\t os.Exit(1) \n }",
			out:      "{ print(errFoo) \n\t os.Exit(1) \n }",
			checkMap: checkMap{},
			handleMap: handleMap{
				1: "errFoo",
			},
		},
		ptc{
			in:  "check atoi(a) + check atoi(b) + check atoi(c)",
			out: "atoi(a) + atoi(b) + atoi(c)",
			checkMap: checkMap{
				1:  true,
				11: true,
				21: true,
			},
			handleMap: handleMap{},
		},
	}
	for _, c := range cases {
		out, cm, hm, err := process(c.in)
		if c.out != out {
			t.Fail()
			fmt.Printf(`expected "%s", but got "%s"\n`, c.out, out)
		}
		if !reflect.DeepEqual(c.checkMap, cm) {
			t.Fail()
			fmt.Printf(`invalid checkMap: %#v\n`, cm)
		}
		if !reflect.DeepEqual(c.handleMap, hm) {
			t.Fail()
			fmt.Printf(`invalid handleMap: %#v\n`, hm)
		}
		if c.error == nil && err != nil {
			t.Fail()
			fmt.Printf(`unexpected error: %#v\n`, err)
		} else if c.error != nil && err == nil {
			t.Fail()
			fmt.Println("expected error, but got nil error")
		}
	}
}
