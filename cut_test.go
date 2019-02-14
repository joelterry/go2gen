package main

import (
	"testing"

	"github.com/joelterry/fun"
)

func TestCut(t *testing.T) {
	x := fun.Test(t, cuts.Apply)
	x.In(cuts{cut{1, 2}}, "hello world").Out("hllo world")
	x.In(cuts{cut{7, 8}, cut{0, 5}}, "hello world").Out(" wrld")
	x.In(cuts{cut{1, 5}, cut{3, 7}}, "hello world").Panic()
}
