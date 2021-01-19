package main

var y int

func a() {
	y++
	b()
}

func b() {
	y--
}

func main() {
	done := make(chan bool)
	x := 0
	c1 := make(chan int)
	c2 := make(chan int)
	go func() {
		select {
		case c1 <- x: // read of x does not race with...
		case c2 <- 1:
		}
		done <- true
	}()
	select {
	case x = <-c1: // ... write to x here
	case c2 <- 1:
		x = 9
	}
	<-done
}
