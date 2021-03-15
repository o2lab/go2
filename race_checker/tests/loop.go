package main

type some_struct struct {
	some_field int
}
func main() {
	for i := 0; i < 2; i++ {
		go func() {
			a := &some_struct{some_field: 0}
			a.some_field++ // no race here but go instruction are identical (wrong context)
		}()
	}
}

