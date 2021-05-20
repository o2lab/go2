package gorace_test

import (
	"fmt"
	"k8s.io/apimachinery/pkg/util/rand"
	"sync"
	"time"

	//"time"
)

var shared = 0

func main() {
	ch1 := make(chan string)
	ch2 := make(chan string)
	ch3 := make(chan string) // unbuffered channel
	x99 := 10
	go func() {
		time.Sleep(3 * time.Second)
		shared = 1
		msg := <-ch2 // corresponding channel receive
		fmt.Println("received ", msg)
		x99 /* RACE Write */ /* RACE Write */ = 2
	}()
	fmt.Println(x99)
	worker(ch1, ch2, ch3)
	if rand.Intn(100) == 2{

	} else {
		x99 /* RACE Read */ ++
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		x99 /* RACE Read */ --
		wg.Done()
	}()
	wg.Wait()
}

func worker(ch1 chan string, chx chan string, ch3 chan string) {
	snd1 := "1st msg"
	snd2 := "2nd msg"
	select {
	case ch1 <- snd1:
		fmt.Println("received ", snd1)
	case chx <- snd2:
		shared = 2
		fmt.Println("sent", snd2)
	case msg := <-ch3:
		fmt.Println("received ", msg)
	}
	fmt.Println(shared)
}
