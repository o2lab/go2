package main

import "fmt"

type ClientCon struct {
	target string
}

func (cc *ClientCon) Close() {
	cc.target = "input1"
}

func DContext(input string) *ClientCon {
	cc := &ClientCon{
		target: input,
	}
	someBool := true
	someInt := 0
	for i2 := 0; i2 < 2; i2++ {
		someInt++
	}

	defer func() { // DContext$1
		if someInt > 0 {
			cc.Close()
			go func() { // DContext$1$1
				someBool = false
			}()
		}
	}()

	if cc.target == "input" {
		someInt++
	} else {
		someInt--
		return cc
	}

	defer func() { // DContext$2
		someBool = false
	}()
	fmt.Println(someBool)
	return cc
}

func main() {
	input := "input"
	DContext(input)
}