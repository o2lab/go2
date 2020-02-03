package main

var (
	i int
)

func main() {
	i = 1
	go func() {
		i = 1
	}()
	foo1(&i)
}

func foo1(i *int) {
	go func() {
		*i = 2
	}()
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
