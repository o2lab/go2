package gorace_test


type some_struct struct {
	some_int int
}
func main() {
	//b := &some_other_struct{some_int: 1}
	for i2 := 0; i2 < 2; i2++ {

		a := &some_struct{some_int: 0} //
		go func() {
			a.some_int++ // false positive here needs to be handled
		}()
	}
}

