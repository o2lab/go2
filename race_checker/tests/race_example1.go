package main

var (
	i int
)

func main() {
	i = 1
	//go func() {
	//	i = 1
	//}()
	foo1()
}

func foo1() {
	go func() {
		i = 2
	}()
}

func aa() {
	return
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
