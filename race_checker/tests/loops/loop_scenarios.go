package main

type mytype struct {
	f string
}

func (r *mytype) invoke()  {
	r.f = r.f + "i also want bug" //read + write
}

func (r *mytype) compare(o *mytype)  {
	if r.f == o.f { //all kinds of read and write
		r.f = o.f
	}else {
		o.f = r.f
	}
}

func static(p *mytype)  {
	p.f = p.f + "i want bug" //read + write
}

//goroutine inside loop vs. var declaration
func main() {
	str := "im clear" //bz: str can be inlined to &mytype{ ... }
	//case 1:
	a := &mytype{str}//bz: change a type to mytype not *mytype
	for i := 1; i < 3; i++ {
		go func() {
			static(a)// static call
			a.invoke()//virtual call
		}()
	}
	//case 2: the following variation 2 can be applied to case 2 to 7
	for i := 1; i < 3; i++ {
		a := &mytype{str}
		go func() { //variation 1
			static(a)
			a.invoke()
		}()
	}
	for i := 1; i < 3; i++ {
		a := &mytype{str}
		go func(a *mytype) {//variation 2
			static(a)
			a.invoke()
		}(a)
	}
	for i := 1; i < 3; i++ {
		go func() {//variation 3
			a := &mytype{str}
			static(a)
			a.invoke()
		}()
	}

	//collections
	var array []*mytype //bz: replace array to map, list, slice, etc; change array type to []mytype
	for i := 1; i < 3; i++ {
		a := &mytype{str}
		array = append(array, a)
	}
	//case 3:
	for _, e := range array {
		go func() {
			static(e)
			e.invoke()
		}()
	}
	//case 4:
	for _, e := range array {
		ee := e
		go func() {
			static(ee)
			ee.invoke()
		}()
	}
	//case 5:
	for i, _ := range array {
		e := array[i]
		go func() {
			static(e)
			e.invoke()
		}()
	}
	//case 6:
	for i := 1; i < len(array); i++ {
		e := array[i]
		go func() {
			static(e)
			e.invoke()
		}()
	}
	//case 7: complex index op
	for i := 1; i < len(array) - 1; i++ {
		e1 := array[i]
		e2 := array[i + 1]
		go func() {
			e1.compare(e2)
		}()
	}
}
