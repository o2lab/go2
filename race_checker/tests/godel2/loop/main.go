package main

func main() {
	ch := make(chan int)
	go helper(ch)
	for i:=0; i<5; i++ {
		ch <- 0
	}
	<-ch
}

func helper(c chan int) {
	for i:=0; i<5; i++ {
		<-c
	}
	close(c)
}
