package main

type some_struct struct {
	some_field int
}
func main() {
	for i2 := 0; i2 < 2; i2++ {
		a := &some_struct{some_field: 0}
		go func() {
			a.some_field++ // false positive here needs to be handled
		}()
	}
}

