package gorace_test


type some_strct struct {
	some_int int
}
func main() {
	//b := &some_other_struct{some_int: 1}
	{a := &some_strct{some_int: 0}
		go func() {
			a.some_int++ // false positive here needs to be handled
		}()}
	//for i2 := 1; i2 < 2; i2++
	{

		a := &some_strct{some_int: 0} //
		go func() {
			a.some_int++ // false positive here needs to be handled
		}()
	}

}

