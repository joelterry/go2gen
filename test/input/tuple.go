// generated by go2gen; DO NOT EDIT

package test

import "fmt"

func multiIn(string, int, bool) {}

func multiOut() (string, int, bool, error) {
	return "", 0, false, nil
}

func tuples() (string, int, bool) {
	_go2string0, _go2int0, _go2bool0, _go2error0 := multiOut()
	if _go2error0 != nil {
		panic(_go2error0)
	}
	s, i, b := _go2string0, _go2int0, _go2bool0
	fmt.Println(s, i, b)
	_go2string1, _go2int1, _go2bool1, _go2error1 := multiOut()
	if _go2error1 != nil {
		panic(_go2error1)
	}
	multiIn(_go2string1, _go2int1, _go2bool1)
	_go2string2, _go2int2, _go2bool2, _go2error2 := multiOut()
	if _go2error2 != nil {
		panic(_go2error2)
	}
	return _go2string2, _go2int2, _go2bool2
}
