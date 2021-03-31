package main

import (
	"sync"
)

type Myfobj struct {
	s string
}

type Myobj struct {
	f Myfobj
}

func (r Myobj) compare(o Myobj, loopid int)  {
	//bz: this print out the addresses that we are going to read/write in the following ifelse branch
	//-> no true race in this .go
	//rf := &r.f
	//of := &o.f
	//fmt.Println("loop  ", loopid, ": r.f: ", &rf, "\t o.f: ", &of)
	if r.f.s == o.f.s {
		r.f = Myfobj{"im different"}
	}else {
		o.f = r.f
	}
	//rf2 := &r.f
	//of2 := &o.f
	//fmt.Println("after ", loopid, ": r.f: ", &rf2, "\t o.f: ", &of2)
}


//func memUsage(m1, m2 *runtime.MemStats) {
//	fmt.Println("Alloc:", m2.Alloc-m1.Alloc,
//		"TotalAlloc:", m2.TotalAlloc-m1.TotalAlloc,
//		"HeapAlloc:", m2.HeapAlloc-m1.HeapAlloc,
//		"#HeapObject:", m2.HeapObjects, " (bytes)")
//}


func main() {
	str := Myfobj{"im clear"}

	//bz: show the memory size of myType
	//fmt.Println("----------loop_obj-----------")
	//var m1, m2, m3, m4, m5, m6 runtime.MemStats
	//runtime.ReadMemStats(&m1)
	//t := Myobj{str}
	//runtime.ReadMemStats(&m2)
	//fmt.Println("sizeof(Myfobj)", unsafe.Sizeof(t.f),
	//	"offset=", unsafe.Offsetof(t.f))
	//fmt.Println("sizeof(string)", unsafe.Sizeof(t.f.s),
	//	"offset=", unsafe.Offsetof(t.f.s))
	//fmt.Println("sizeof(Myobj)", unsafe.Sizeof(t))
	//fmt.Println("----------------------------")
	//
	//fmt.Println("-----------Heap-------------")
	var array []Myobj //bz: str, array, a are all objects on stack ... !!!
	for i := 1; i < 5; i++ {
		//runtime.ReadMemStats(&m3)
		a := Myobj{str}
		//runtime.ReadMemStats(&m4)
		//memUsage(&m3, &m4)
		array = append(array, a)
	}
	//fmt.Println("----------------------------")

	var wg sync.WaitGroup
	for i := 0; i < len(array) - 1; i++ {
	//for ii, e1 := range array {
		//bz: ii, e1, e2 declared in loop, but values are passed from array
		ii := i
		//runtime.ReadMemStats(&m5)
		e1 := array[ii]
		//runtime.ReadMemStats(&m6)
		//memUsage(&m5, &m6)
		e2 := array[ii + 1]

		////bz: check the following addresses are different if array element is object
		//_e1 := &array[i]
		//_e2 := &array[i + 1]
		//fmt.Println("assign", ii, ": [", i, "]: ", &_e1, "  [", i+1, "]: ", &_e2)
		//
		//e1f := &array[i].f
		//e2f := &array[i + 1].f
		//fmt.Println("assign", ii, ": [", i, "].f: ", &e1f, "  [", i+1, "].f: ", &e2f)
		//
		//_r := &e1
		//_o := &e2
		//fmt.Println("assign", ii, ": e1: ", &_r, "\t  e2: ", &_o)
		//
		//rf := &e1.f
		//of := &e2.f
		//fmt.Println("assign", ii, ": e1.f: ", &rf, "\t  e2.f: ", &of)

		wg.Add(1)
		go func() {
			e1.compare(e2, ii)
			wg.Done()
		}()
	}
	wg.Wait()

}
/*
go run -gcflags -m loop_obj.go -> escape analysis
go run -race loop_obj.go
 */
