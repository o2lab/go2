package gorace_test

import "fmt"

type ClientC struct {
	target string
}

func (cc *ClientC) Close() {
	cc.target = "input1"
}

func DContext1(input string) *ClientC {
	cc := &ClientC{
		target: input,
	}
	someBool := true
	someInt := 0
	for i2 := 0; i2 < 2; i2++ {
		someInt++
	}

	defer func() {
		if someInt > 0 {
			cc.Close()
			go func() {
				someBool = false
			}()
		}
	}()

	if cc.target == "input" {
		someBool = true
		fmt.Println(someBool)
		return cc
	} else {
		someBool = false
		fmt.Println(someBool)
		return cc
	}
}

func main() {
	input := "input"
	DContext1(input)
}