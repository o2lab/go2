package main

var (
	i int
	j int
)

func main() {
	i = 1
	go func() {
		i = 1
	}()
	i = 2
}

//func A() {
//	i = j + 1
//	go func() {
//		B()
//	}()
//}
//
//func B() {
//	j = i + 1
//	A()
//}