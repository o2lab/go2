package main

import (
	"fmt"
	"google.golang.org/grpc"
)

func someStream() string {
	someInfo := "blah"
	return someInfo + "er"
}

func someFunc(cs1 grpc.ClientStream) int {
	err23 := cs1.SendMsg(someStream())
	if err23 != nil {
		return 1
	}
	return 0
}

func main() {
	var cs1 grpc.ClientConn
	stream, err := cs1.NewStream()
	_ = err
	go someFunc(stream)
	var err1 = stream.CloseSend()
	if err1 != nil {
		fmt.Println(err1)
	}
}