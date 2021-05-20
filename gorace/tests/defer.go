package gorace_test

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

	defer func() {
		if someInt > 0 {
			cc.Close()
			go func() {
				someBool /* RACE Write */ = false
			}()
		}
	}()

	if cc.target == "input" {
		someInt++
	} else {
		someInt--
		func() {
			if someInt > 0 {
				cc.Close()
				go func() {
					someBool = false
				}()
			}
		}()
		return cc
	}


	defer func() {
		someBool = false
		go func() {
			someBool /* RACE Write */ = true
		}()
	}()
	fmt.Println(someBool)
	func() {
		someBool = false
		go func() {
			someBool = true
		}()
	}()
	return cc
}

func main() {
	input := "input"
	DContext(input)
}