package main

import (
	"sync"
)

type myField struct {
	s string
}

type myType struct {
	f *myField
}

func (r *myType) compare(o *myType, loopid int) {
	//bz: this print out the addresses that we are going to read/write in the following ifelse branch
	//-> run default race checker multiple times, most times it misses the races
	//fmt.Println("loop  ", loopid, ": r.f: ", &r.f, "\t o.f: ", &o.f)
	if r.f.s == o.f.s { //read r.f.s, read o.f.s
		r.f = o.f //read o.f, write r.f
	} else {
		o.f = r.f //read r.f, write o.f
	}
	//fmt.Println("after ", loopid, ": r.f: ", &r.f, "\t o.f: ", &o.f)
}

//func memUsage2(m1, m2 *runtime.MemStats) { //bytes
//	fmt.Println("Alloc:", m2.Alloc-m1.Alloc,
//		"TotalAlloc:", m2.TotalAlloc-m1.TotalAlloc,
//		"HeapAlloc:", m2.HeapAlloc-m1.HeapAlloc, " (bytes)")
//}

func main() {
	str := &myField{"im clear"}

	//bz: show the memory size of myType
	//fmt.Println("-------loop_pointer--------")
	//var m1, m2, m3, m4 runtime.MemStats
	//runtime.ReadMemStats(&m1)
	//t := myType{str}
	//runtime.ReadMemStats(&m2)
	//fmt.Println("sizeof(*myField)", unsafe.Sizeof(t.f),
	//	"offset=", unsafe.Offsetof(t.f))
	//fmt.Println("sizeof(string)", unsafe.Sizeof(t.f.s),
	//	"offset=", unsafe.Offsetof(t.f.s))
	//fmt.Println("sizeof(myType)", unsafe.Sizeof(t))
	//fmt.Println("----------------------------")

	//fmt.Println("-----------Heap-------------")
	var array []*myType //bz: str, array, a are all pointers and objects on heap !!!
	for i := 1; i < 5; i++ {
		//runtime.ReadMemStats(&m3)
		a := &myType{str}
		//runtime.ReadMemStats(&m4)
		//memUsage2(&m3, &m4)
		array = append(array, a)
	}
	//fmt.Println("----------------------------")

	var wg sync.WaitGroup
	//for i := 0; i < len(array) - 1; i++ {
	for ii, e1 := range array {
		////bz: ii, e1, e2 declared in loop, but values are passed from array
		//ii := i
		//e1 := array[ii]
		e2 := array[ii+1]

		////bz: check the following addresses are different if array element is object
		//e1f := &array[i]
		//e2f := &array[i + 1]
		//fmt.Println("assign", ii, ": [i].f: ", &e1f, "  [+1].f: ", &e2f)
		//
		//rf := &e1
		//of := &e2
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
go run -gcflags -m loop_pointer.go -> escape analysis
go run -race loop_pointer.go
*/
