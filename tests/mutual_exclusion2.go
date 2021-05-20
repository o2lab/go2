package main

import (
	"fmt"
	"k8s.io/apimachinery/pkg/util/rand"
)
type bObj struct {
	anInt 	int
}
func bFunc(theObj *bObj) {
	go func() {
		theObj.anInt++
	}()
}
func main() {
	sn := &bObj{anInt: 1}
	//sn1 := &bObj{anInt: 2}
	if rand.Intn(100) != 0 {
		bFunc(sn)
	} else {
		bFunc(sn)
	}
	fmt.Println("r")
}
