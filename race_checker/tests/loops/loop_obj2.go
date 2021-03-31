package main

import (
	"sync"
)

type Myfobj2 struct {
	s string
}

type Myobj2 struct {
	f Myfobj2
}

func (r Myobj2) compare(o *Myobj2, loopid int) {
	//bz: this print out the addresses that we are going to read/write in the following ifelse branch
	//rf := &r.f
	//of := &o.f
	//fmt.Println("loop  ", loopid, ": r.f: ", &rf, "\t o.f: ", &of)
	if r.f.s == o.f.s {
		r.f = Myfobj2{"im different"}
	} else {
		o.f = r.f
	}
	//rf2 := &r.f
	//of2 := &o.f
	//fmt.Println("after ", loopid, ": r.f: ", &rf2, "\t o.f: ", &of2)
}


func main() {
	str := Myfobj2{"im clear"}

	var array []Myobj2 //bz: str, array, a are all objects on stack!!!
	for i := 1; i < 5; i++ {
		a := Myobj2{str}
		array = append(array, a)
	}

	var wg sync.WaitGroup
	//for i, e1 := range array  {
	for i := 0; i < len(array) - 1; i++ {
		//bz: use i, e1 from array range iteration
		//bz: check the following addresses are different if array element is object
		//e1f := &array[i].f
		//fmt.Println("assign", i, ": [i].f: ", &e1f)
		//
		//rf := &e1.f
		//fmt.Println("assign", i, ": e1.f: ", &rf)
		e1 := array[i]

		wg.Add(1)
		go func() {
			e1.compare(&e1, i)
			wg.Done()
		}()
	}
	wg.Wait()

}