package gorace_test

import (
	"fmt"
	"k8s.io/apimachinery/pkg/util/rand"
)
type aObj struct {
	anInt 	int
}
func aFunc(theObj *aObj) {
	go func() {
		theObj.anInt++
	}()
}
func main() {
	sn := &aObj{anInt: 1}
	if rand.Intn(100) != 0 {
		aFunc(sn)
	} else {
		sn.anInt++
	}
	fmt.Println("end")
}
