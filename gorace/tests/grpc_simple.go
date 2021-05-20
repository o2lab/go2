package gorace_test

import (
	"context"
	"fmt"
	"google.golang.org/grpc"
)

func someFunc(cs1 grpc.ClientStream) int {
	err23 := cs1.SendMsg(cs1)
	if err23 != nil {
		return 1
	}
	return 0
}

func main() {
	var cs1 grpc.ClientConn
	var ctx context.Context
	var desc *grpc.StreamDesc
	var method string
	var opts grpc.CallOption
	stream, err := cs1.NewStream(ctx, desc, method, opts)
	if err != nil {
		fmt.Println(err)
	}
	go someFunc(stream)
	var err1 = stream.CloseSend()
	if err1 != nil {
		fmt.Println(err1)
	}
}