package main

type myftype struct {
	s string
}

type mytype struct {
	f *myftype
}

func (r *mytype) invoke()  {
	r.f.s = r.f.s + "i also want bug" //read + write
}

func (r *mytype) compare(o *mytype)  {
	if r.f == o.f { //all kinds of read and write
		r.f = o.f
	}else {
		o.f = r.f
	}
}

//goroutine inside loop vs. var declaration: see below
func main() {
	case1()
	case2()
	case3()
	case4()
	case5()
	case6()
	case7()
}

func case1() {
	str := &myftype{"im clear"} //bz: str can be inlined to &mytype{ ... }
	//case 1:
	a := &mytype{str}//bz: change a type to mytype not *mytype
	for i := 1; i < 3; i++ {
		go func() {
			a.invoke()//race on a
		}()
	}
}

func case2() {
	str := &myftype{"im clear"} //bz: str can be inlined to &mytype{ ... }
	//case 2: the following variation 2 can be applied to case 2 to 7
	for i := 1; i < 3; i++ {
		//str := &myftype{"im clear"} //bz: str inlined
		a := &mytype{str}
		go func() { //variation 1
			a.invoke()//race on a.f.s
		}()
	}
	for i := 1; i < 3; i++ {
		//str := &myftype{"im clear"} //bz: str inlined
		a := &mytype{str}
		go func(a *mytype) {//variation 2: a as parameter
			a.invoke() //race on a.f.s
		}(a)
	}
	for i := 1; i < 3; i++ {
		go func() {//variation 3
			//str := &myftype{"im clear"} //bz: str inlined
			a := &mytype{str}
			a.invoke()//race on a.f.s
		}()
	}
}

func case3()  {
	str := &myftype{"im clear"} //bz: str can be inlined to &mytype{ ... }
	//collections
	var array []*mytype //bz: replace array to map, list, slice, etc; change array type to []mytype
	for i := 1; i < 3; i++ {
		//str := &myftype{"im clear"} //bz: str inlined
		a := &mytype{str}
		array = append(array, a)
	}
	//case 3: race
	for _, e := range array {
		go func() {
			e.invoke()//race on e and the e in the for range, race on the base var of invoke
		}()
	}
}

func case4()  {
	str := &myftype{"im clear"} //bz: str can be inlined to &mytype{ ... }
	//collections
	var array []*mytype //bz: replace array to map, list, slice, etc; change array type to []mytype
	for i := 1; i < 3; i++ {
		//str := &myftype{"im clear"} //bz: str inlined
		a := &mytype{str}
		array = append(array, a)
	}
	//case 4:
	for _, e := range array {
		ee := e
		go func() {
			ee.invoke() //race on ee.f.s
		}()
	}
}

func case5()  {
	str := &myftype{"im clear"} //bz: str can be inlined to &mytype{ ... }
	//collections
	var array []*mytype //bz: replace array to map, list, slice, etc; change array type to []mytype
	for i := 1; i < 3; i++ {
		//str := &myftype{"im clear"} //bz: str inlined
		a := &mytype{str}
		array = append(array, a)
	}
	//case 5:
	for i, _ := range array {
		e := array[i]
		go func() {
			e.invoke() //race on ee.f.s
		}()
	}
}

func case6()  {
	str := &myftype{"im clear"} //bz: str can be inlined to &mytype{ ... }
	//collections
	var array []*mytype //bz: replace array to map, list, slice, etc; change array type to []mytype
	for i := 1; i < 5; i++ {
		//str := &myftype{"im clear"} //bz: str inlined
		a := &mytype{str}
		array = append(array, a)
	}
	//case 6:
	for i := 1; i < len(array); i++ {
		e := array[i]
		go func() {
			e.invoke() //race on e.f.s
		}()
	}
}

func case7()  {
	str := &myftype{"im clear"} //bz: str can be inlined to &mytype{ ... }
	//collections
	var array []*mytype //bz: replace array to map, list, slice, etc; change array type to []mytype
	for i := 1; i < 5; i++ {
		//str := &myftype{"im clear"} //bz: str inlined
		a := &mytype{str}
		array = append(array, a)
	}
	//case 7: complex index op
	for i := 1; i < len(array) - 1; i++ {
		e1 := array[i]
		e2 := array[i + 1]
		go func() {
			e1.invoke()
			e2.invoke()//race on e.f.s
			e1.compare(e2)//race on e.f
		}()
	}
}