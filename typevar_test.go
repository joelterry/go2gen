package main

import (
	"testing"

	"github.com/joelterry/fun"
)

func TestTypeToVar(t *testing.T) {
	f := fun.Test(t, typeToVar)
	f.In("foo").Out("foo")
	f.In("pkg.foo").Out("foo")
	f.In("*foo").Out("ptrFoo")
	f.In("**foo").Out("ptrPtrFoo")
	f.In("[]foo").Out("slcFoo")
	f.In("[123]foo").Out("arrFoo")
	f.In("[][][123]**foo").Out("slcSlcArrPtrPtrFoo")
	f.In("map[int]bool").Out("mapOfIntToBool")
	f.In("map[map[int]bool][123]*foo").Out("mapOfMapOfIntToBoolToArrPtrFoo")
}
