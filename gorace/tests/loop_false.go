package gorace_test

type some_struct struct {
	some_other *some_other_struct
}

type some_other_struct struct {
	some_int int
}
func main() {
	b := &some_other_struct{some_int: 1}
	for i2 := 0; i2 < 2; i2++ {

		a := &some_struct{some_other: b}
		go func() {
			a.some_other.some_int++ // false positive here needs to be handled
		}()
	}
}

