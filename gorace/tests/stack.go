package main

import (
	"fmt"
	"sync"
)
type anyObj struct {
	anInt 	int
}
func increField(theObj *anyObj) {
	theObj.anInt /* RACE Read */ /* RACE Write */ ++
}
func main() {
	sn := &anyObj{
		anInt: 1,
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		go func() {
			increField(sn)
		}()
		wg.Done()
	}()
	increField(sn)
	wg.Wait()
	fmt.Println(sn)
}