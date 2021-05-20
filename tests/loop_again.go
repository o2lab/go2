package main

type a4 struct {
	a5 	int
}
func main() {
	a8 := &a4{a5: 8}
	for j2 := 0; j2 < 3; j2++ {
		go func() {
			a8.a5++
		}()

	}
}