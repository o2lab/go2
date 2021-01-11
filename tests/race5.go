package main

func main() {
	x := 1
	f := func() {
		x = 2
	}
	DoAsync(f)
	_ = x
}

func DoAsync(callback func()) {
	go func() {
		callback()
	}()
}