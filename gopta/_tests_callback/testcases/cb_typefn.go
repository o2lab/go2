// +build ignore

package main

import (
	"fmt"
	"math/rand"
	"github.com/april1989/origin-go-tools/_tests_callback/lib"
)

func Myfn1(i int) {// @pointsto i@main.Myfn1=t0@main.main
	fmt.Printf("\ni is %v", i)
}
func Myfn2(i int) {// @pointsto i@main.Myfn2=t1@main.main
	fmt.Printf("\ni is %v", i)
}

func main() {
	p1 := rand.Int()
	p2 := rand.Int()
	lib.Test(Myfn1, p1)
	lib.Test(Myfn2, p2)
}