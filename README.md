# Go2 Race Detector

![Tests](https://github.com/o2lab/go2/workflows/Tests/badge.svg)

## Static Analysis - Concurrency Bug Detection in Go
### Data Races

The tool makes use of __static single-assignment (SSA)__ form intermediate representation to construct goroutine-specific __stack traces__ discretely for the basis of static analysis. Throughout the process of constructing each stack trace, Andersen's inclusion-based __pointer analysis__ algorithm is used on an as-need basis[1]. Due to the flow-insensitive and context-insensitive nature of the adopted algorithm, there's potential for ambiguity in the points-to information returned by the analysis[2]. Upon completing the construction of stack traces, a __Happens-Before graph__ is built to enforce flow-sensitive analysis. Ultimately, racy instructions are reported, each along with its corresponding call stack. 

**[3]

****************************************************************************************************
_Work in Progress:_

[1] such repetitive procedures are significantly costing performance of the tool. A more efficient algorithm may be put in place to minimize frequency of conducting pointer analysis. 

[2] for the time being, in the case of multiple targets being returned by pointer analysis, the call stack is used to determine which target in the points-to set to execute. Need a much more refined method to consider calling context of functions in the code base. 

[3] need to consider how Go handles communication and synchronization more precisely. 


## Combining Channels with Select

Channel operations are an elegant feature of Go in allowing communication among goroutines. By combining them with select, a goroutine is able to wait on multiple communication operations. 

### Case 1 - Typical Channel Operations (Blocking): 

Channel sends and receives as case statements will block until they become valid. ie. channel receives must contain available value; channel sends must have corresponding receive or the channel is buffered. 

```
ch1 := make(chan string)
ch2 := make(chan string)
ch3 := make(chan string)

snd1 := "1st msg"
snd2 := "2nd msg"

go func() {
  msg := <-ch2
  fmt.Println("received ", msg)
}()

go func() {
  ch3 <- "3rd msg"
}()

select {
case ch1 <- snd1:
  fmt.Println("sent ", snd1)
case ch2 <- snd2:
  fmt.Println("sent", snd2)
case msg := <- ch3:
  fmt.Println("received ", msg)
}

```
In cases where multiple cases are valid, select will choose one pseudo-randomly (depends on runtime order of execution). 

Output: 

*received  2nd msg*

*sent 2nd msg*


### Handling Typical Channel Ops:

Anaylze all valid cases for potential races. Ignore instructions under blocking cases. 

### Case 2 - Awaiting multiple channels: 

```
message1 := make(chan string)
	message2 := make(chan string)
	msg1 := "hi"
	msg2 := "hi again"
	go func() {
		time.Sleep(1*time.Second) 
		message1 <- msg1 // becomes ready first
	}()
	go func() {
		time.Sleep(2*time.Second)
		message1 <- msg2
	}()
	for i := 0; i < 2; i++ {
		select {
		case a := <- message1:
			fmt.Println("sent message", a)
		case b := <- message2:
			fmt.Println("sent another", b)
		}
	}

```
Each iteration of forloop will print different result. 

Output: 

*sent message hi*

*sent message hi again*


### Handling For Loops:

Unroll for loops twice. (typ.) 


### Case 3 - Timeouts: 

```
ch1 := make(chan string)
	ch2 := make(chan string)
	go func() {
		time.Sleep(2*time.Second) // greater than timeout allowance
		ch1 <- "just in time - 1st attempt"
	}()
	select {
	case check := <-ch1:
		fmt.Println(check)
	case <-time.After(1*time.Second):
		fmt.Println("timeout - 1st attempt")
	}

	go func() {
		time.Sleep(3*time.Second) // shorter than timeout allowance
		ch2 <- "just in time - 2nd attempt"
	}()
	select {
	case check := <-ch2:
		fmt.Println(check)
	case <-time.After(4*time.Second):
		fmt.Println("timeout - 2nd attempt")
	}
```
Relieve blocked select after set amount of time. 

Output: 

*timeout - 1st attempt*

*just in time - 2nd attempt*


### Handling Timeouts:

Always handle instructions in timeout clause, in addition to any case statements that are valid. 

### Case 4 - Non-Block Channel Operations:

```
ch1 := make(chan int)
	ch2 := make(chan int)
	x := 0
	go func() {
		x /* RACE Write */= 1
		ch1 <- 1
	}()
	select {
	case a := <-ch1: // this case is ready
		x = a
	case a := <-ch2:
		x = a + 1
	default:
		x /* RACE Write */= 20
		fmt.Println(x)
	}
```
Both the first case and the default case may be executed. 

Output:

*20*


### Handling the Default Clause:

Always handle instructions in default clause, in addition to any case statements that are valid. 
### Case 5 - No Selected Cases in Blocking Select Operations:

```
ch1 := make(chan int)
	ch2 := make(chan int)
	x := 0
	go func() {
		x = 1
	}()
	select {//no cases are ready
	case a := <-ch1: 
		x = a
	case a := <-ch2:
		x = a + 1
	fmt.Println(x)
	}
```
Program blocks at the select, because has no corresponding send operations or default. 

Output:

*nothing(runs forever due to frozen select)*


### Handling the Stalled Blocking Select Clause:
instructions after and in the select for the go routinue do NOT happen(don't analyse those instructions for race detection, because won't happen). Go routines called before select DO happen. Perhaps tell user the select was Stalled?

