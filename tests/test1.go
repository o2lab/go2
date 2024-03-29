//package main
//
//import (
//	"fmt"
//)
//
//var k int
//var xx int
//
//func getNumber() int {
//	var i int
//	xx = 2
//	go writeI(&i)
//	return i // racy read, race #3
//}
//func writeI(j *int) {
//	*j = 5 // racy write, race #3
//}
//
//type S struct {
//	i int
//}
//
//func (s *S) read() int {
//	return s.i // racy read, race #1
//}
//func (s *S) write(i int) {
//	s.i = i // racy write, race #1
//}
//
//func f1(i int) int {
//	j := i
//	j--
//	return j
//}
//
//func f2(h *int) {
//	*h++ // racy write, race #2
//}
//
//func main() {
//	s := &S{
//		i: 1,
//	}
//	go func() {
//		s.write(12)
//	}()
//	s.read()
//
//	fmt.Println(getNumber())
//
//	go func() {
//		_ = k // racy read, race #4
//	}()
//	k = 2 // racy write, race #4
//
//	i := 1
//	go f2(&i)
//	j := f1(i) // racy read, race #2
//	fmt.Println(j)
//}

// ___________________________________________________

//Case 7 (channels)
//package main
//
//import (
//	"fmt"
//	"sync"
//)
//
//var chn = make(chan int, 1)
//var wg = sync.WaitGroup{}
//
////var t = 2
//
//func Y(t *int) {
//	*t = 1
//	//j := *t
//	chn <- 1
//	//j = *t
//	go func() {
//		<-chn
//		//*t = 1
//		wg.Done()
//	}()
//}
//
//func main() {
//	t := 2
//	go Y(&t)
//	wg.Wait()
//	fmt.Println(t)
//}

//Case 8 (WaitGroup)
//package main
//
//import (
//	"fmt"
//	"sync"
//)
//
//var w int
//var v int
//
//func worker(wg *sync.WaitGroup) {
//	fmt.Printf("K value is %d \n", w) //racy read, race #5
//	wg.Done()
//}
//
//func main() {
//	v = 4
//	var wg sync.WaitGroup
//	wg.Add(1)
//	go worker(&wg)
//	wg.Wait()
//	w = 0  //racy write, race #5
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

//Case 5 (HAPPENS-BEFORE: GOOD)
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

// Testing method calls
//package main
//
//import (
//	"fmt"
//)
//
//type someData struct {
//	someInt int
//}
//
//var xtoy = make(chan int)
//
//func (someNum someData) justRecv() int {
//	k := <-xtoy
//	close(xtoy)
//	return k
//}
//
//func (someNum someData) justSend() {
//	xtoy <- someNum.someInt
//}
//
//func main() {
//	x2 := someData{someInt: 5}
//	var j int
//	go func() {
//		x2.someInt = 2 * x2.someInt
//		x2.justSend()
//	}()
//	go func() {
//		j = x2.justRecv()
//		fmt.Println(j)
//	}()
//}
package main

import "fmt"

func main() {
	var m02 map[int]int
	m02 = make(map[int]int)
	for j02 := 0; j02 < 3; j02++ {
		m02[j02] = j02 + 1
	}
	for _, i02 := range m02 {
		fmt.Println("Let's see ", i02)
	}
}
