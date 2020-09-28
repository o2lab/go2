package main

import (
	"sync"
	"testing"
)

type MutableTableDescriptor struct {
	TableDescriptor
}

func (*MutableTableDescriptor) FindCheckByName(name string) {}

func NewMutableExistingTableDescriptor(tbl TableDescriptor) *MutableTableDescriptor {
	return &MutableTableDescriptor{TableDescriptor: tbl}
}

func validateCheckInTxn(tableDesc *MutableTableDescriptor, checkName *string) {
	tableDesc.FindCheckByName(*checkName) // racy read on checkName
}

type ConstraintToValidate struct {
	Name string
}

type SchemaChanger struct{}

type Descriptor struct{}

type TableDescriptor struct{}

func (*Descriptor) GetTable() *TableDescriptor {
	return &TableDescriptor{}
}

func GetTableDescFromID() *TableDescriptor {
	desc := &Descriptor{}
	return desc.GetTable()
}

type ImmutableTableDescriptor struct {
	TableDescriptor
}

func NewImmutableTableDescriptor(tbl TableDescriptor) *ImmutableTableDescriptor {
	return &ImmutableTableDescriptor{TableDescriptor: tbl}
}

func (desc *ImmutableTableDescriptor) MakeFirstMutationPublic() *MutableTableDescriptor {
	return NewMutableExistingTableDescriptor(desc.TableDescriptor)
}

func (*SchemaChanger) validateChecks(checks []ConstraintToValidate) {
	func() {
		tableDesc := GetTableDescFromID()
		desc := NewImmutableTableDescriptor(*tableDesc).MakeFirstMutationPublic()
		for _, c /* RACE Write */ := range checks { // racy write on c (Name field)
			go func() {
				validateCheckInTxn(desc, &c.Name /* RACE Read */) // will trigger racy read
			}()
		}
	}()
}

func (sc *SchemaChanger) runBackfill() {
	var checksToValidate []ConstraintToValidate
	for i := 0; i < 10; i++ {
		checksToValidate = append(checksToValidate, ConstraintToValidate{Name: "nil string"})
	}
	sc.validateChecks(checksToValidate)
}

func TestCockroach35501(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := &SchemaChanger{}
		sc.runBackfill() // triggers racy write and spawns child goroutine that triggers racy read
	}()
	wg.Wait()
}
func main() {
	var t *testing.T
	TestCockroach35501(t)
}