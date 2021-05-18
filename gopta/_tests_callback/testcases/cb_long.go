// +build ignore

package main

import (
	"fmt"
	"github.com/april1989/origin-go-tools/_tests_callback/lib"
	rand2 "math/rand"
)


func getCallBack(b *lib.Wrapper) func() {// @pointsto b@main.getCallBack=t2@main.main
	return func() { fmt.Println(b.B == true) }
}

func main() {
	var abort *lib.Wrapper
	rand := rand2.Int()
	if rand > 1 {
		abort = &lib.Wrapper{ B: true }
	} else {
		abort = &lib.Wrapper{ B: false}
	}
	f := getCallBack(abort)
	lib.Level1(f, abort) //pass as a pointer with free var: abort
}
