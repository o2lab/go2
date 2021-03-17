package main

type some_struct struct {
	some_field int
}
func main() {
	for i2 := 0; i2 < 2; i2++ {
		go func() { // context
			a := &some_struct{some_field: 0}
			a.some_field++ // no race here but go instruction are identical (wrong context)
		}()
	}
}

