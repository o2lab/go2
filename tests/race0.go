package main

var y int

func a() {
	y++
	b()
}

func b() {
	y--
}

func main() {
	x := 1
	go func() {
		x = 2
		a()

	}()
	_ = x
	func() {
		func () {
			a()
		}()
	}()
}