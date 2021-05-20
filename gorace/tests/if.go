package gorace_test

import (
	"fmt"
	"k8s.io/apimachinery/pkg/util/rand"
	"sync"
)
type anObj struct {
	anInt 	int
}
func incrField(theObj *anObj) {
	theObj.anInt++
}
func AAA(sn *anObj) {
	incrField(sn)
}
func BBB(sn *anObj) {
	go AAA(sn)
}
func main() {
	sn := &anObj{
		anInt: 1,
	}
	var wg sync.WaitGroup
	if rand.Intn(100) != 0 {
		go BBB(sn)
		//go func() {
		//	go func() {
		//		incrField(sn)
		//	}()
		//}()
	} else {
		wg.Add(1)
		go func() {
			incrField(sn)
			wg.Done()
		}()
	}
	wg.Wait()
	fmt.Println(sn.anInt)
}
