//TestCockroach27659
package main

import (
	"sync"
	"testing"
)

type Timestamp struct {
	WallTime uint64
	Logical  int32
}

type Sender interface {
	Send()
}

type TxnSender interface {
	Sender
}

type Txn struct {
	deadline *Timestamp
}

func (txn *Txn) resetDeadline() {
	txn.deadline /* RACE Write */ = nil // racy write on deadline field
}
func (txn *Txn) updateStateOnRetryableErrLocked() {
	txn.resetDeadline()
}

func (txn *Txn) Send() {
	txn.updateStateOnRetryableErrLocked()
}

func (txn *Txn) Run() {
	sendAndFill(txn.Send)
}

func (txn *Txn) UpdateDeadlineMaybe() {
	if txn.deadline /* RACE Read */ == nil {
	}
}

type SenderFunc func()

func sendAndFill(send SenderFunc) {
	send()
}

func TestCockroach27659(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		txn := &Txn{deadline: &Timestamp{}}
		go func() {
			defer wg.Done()
			txn.Run() // triggers racy write
		}()
		txn.UpdateDeadlineMaybe() // triggers racy read
	}()
	wg.Wait()
}

func main() {
	var t *testing.T
	TestCockroach27659(t)
}
