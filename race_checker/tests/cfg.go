package main

func main() {
	x := 0
	for i := 0; i < 10; i++ {
		x++
		if i == 1 {
			return
		} else {
			panic("a")
		}
	}
}
