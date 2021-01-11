package main

type S struct {
	i int
}

func main() {
	SSlice := []S{S{0}}
	for _, s := range SSlice{
		go func() {
			_ = s.i
		}()
	}
}
