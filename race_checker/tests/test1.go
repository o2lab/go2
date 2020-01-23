//Case 1
//package main
//import "fmt"
//func main() {
//	fmt.Println(getNumber())
//}
//func getNumber() int {
//	var i int
//	go writeI(&i)
//	return i // racy read
//}
//func writeI(j *int) {
//	*j = 5 // racy write
//}

//Case 2
//package main
//type S struct {
//	i int
//}
//func (s *S) read() int { // not anonymous
//	return s.i // racy read
//}
//func (s *S) write(i int) { // not anonymous
//	s.i = i // racy write
//}
//func main() {
//	s := &S{
//		i: 1,
//	}
//	go func() {
//		s.write(12)
//	}()
//	s.read()
//}

//Case 3
//package main
//var i int // global variable
//func main() {
//	go func() {
//		i = 1 // racy write
//	}()
//	i = 2 //racy write
//}

//Case 4 (HAPPENS-BEFORE: BAD)
//package main
//import "fmt"
//func f1(i int) int {  // pure function
//	j := i
//	j--
//	return j
//}
//
//func f2(h *int) {
//	*h++ // racy write
//}
//
//func main() {
//	i := 1
//	go f2(&i)
//	j := f1(i) // racy read
//	fmt.Println(j)
//}

//Case 5 (HAPPENS-BEFORE: GOOD) TODO: Check function summary Count!
//package main
//import "fmt"
//func f1(ch chan int) int {
//	j := <-ch
//	j--
//	return j
//}
//
//func f2(i int, ch chan int) {
//	i++
//	ch <- i
//}
//
//func main() {
//	i := 1
//	ch := make(chan int)
//	go f2(i, ch)
//	j := f1(ch)
//	fmt.Println(j)
//}

//Case 6 (NO race; non-deterministic select statement)
//package main
//
//import (
//	"fmt"
//)
//
//func f2(i int, ch chan int) (int, int) {
//	var j int
//	go func() {
//		j = i
//		ch <- j
//	}()
//	select {
//	case j = <-ch:
//		j++
//	case i = <-ch:
//		i++
//	}
//	return i, j
//}
//
//func main() {
//	i := 1
//	ch := make(chan int)
//	var k int
//	var j int
//	k, j = f2(i, ch)
//	fmt.Println(k)
//	fmt.Println(j)
//}

//Case 7 (from Professor)
//package main
//import "fmt"
//
//func main() {
//	fmt.Println(getNum())
//}
//func getNum() int {
//	var i int
//	go func() {
//		i = 5
//	}()
//	return i
//}