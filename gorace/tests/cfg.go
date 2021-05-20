package gorace_test

func main() {
	x12 := 0
	for i2 := 0; i2 < 10; i2++ {
		x12++
		if i2 == 1 {
			return
		} else {
			panic("a")
		}
	}
}
