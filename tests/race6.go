package main

type TT struct {
	a, b, c, d int
}

func main() {
	t := TT{}
	go func() {
		t.a = 1
	}()
	_ = t.a
	_ = t.b
}
