// generated by go2gen; DO NOT EDIT

package test

import (
	"fmt"
)

func field() error {
	_go2int0, _go2error0 := func() (int, error) {
		return 20, nil
	}()
	if _go2error0 != nil {
		return _go2error0
	}
	x := struct {
		a int
		b int
	}{
		a: 10,
		b: _go2int0,
	}
	fmt.Println(x)
	return nil
}
