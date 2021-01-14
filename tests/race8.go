package main

func main() {
	x := 0
	go func() {
		x = 1
	}()
	_ = x
}
