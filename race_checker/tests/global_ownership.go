package main

var j int

func main() {
	go func() {
		_ = j
	}()
	j = 1
}
