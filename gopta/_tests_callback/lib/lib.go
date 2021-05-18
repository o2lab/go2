package lib

import "time"

//bz: lib func for cb_typefn.go
type fn func(int)

//func Myfn1(i int) {
//	fmt.Printf("\ni is %v", i)
//}
//func Myfn2(i int) {
//	fmt.Printf("\ni is %v", i)
//}

func Test(f fn, val int) {
	f(val)
}


//bz: lib func for cb_namefn.go
//func Square(num int) int {
//	return num * num
//}

func Mapper(f func(int) int, alist []int) []int {
	var a = make([]int, len(alist), len(alist))
	for index, val := range alist {
		a[index] = f(val)
	}
	return a
}

//bz: lib type for cb_long.go
type Wrapper struct {
	B  bool
}

//bz: lib func for cb_long.go
func Level1(f1 interface{}, b1 *Wrapper) { //cast to interface
	level2(f1, b1)
}

func level2(f2 interface{}, b2 *Wrapper) {
	level3(f2, b2)
}

func level3(f3 interface{}, b3 *Wrapper) {
	level4(f3, b3)
}

func level4 (f4 interface{}, b4 *Wrapper) {
	if b4 != nil {
		time.AfterFunc(time.Nanosecond, f4.(func())) //cast back
	}
}