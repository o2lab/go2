package analyzer

import (
	log "github.com/sirupsen/logrus"
	"go/token"
	"golang.org/x/tools/go/pointer"
	"golang.org/x/tools/go/ssa"
)

type fnSummary struct {
	fset *token.FileSet
	accessSet map[ssa.Value]IsWrite
	allocSet map[*ssa.Alloc]ssa.Instruction
}

type IsWrite bool

func (f *fnSummary) summarize(function *ssa.Function, ptaConfig *pointer.Config) {
	for _, block := range function.Blocks {
		for _, instr := range block.Instrs {
			log.Debugln(instr)
			f.visitIns(instr)
		}
	}
	for loc, _ := range f.accessSet {
		ptaConfig.AddQuery(loc)
	}
}

func (f *fnSummary) visitIns(instruction ssa.Instruction) {
	switch instr := instruction.(type) {
	case *ssa.UnOp:
		f.visitUnOp(instr)
	case *ssa.Store:
		f.visitStore(instr)
	case *ssa.MapUpdate:
		f.visitMapUpdate(instr)
	case *ssa.Lookup:
		f.visitLookup(instr)
	case *ssa.Alloc:
		f.visitAlloc(instr)
	case *ssa.IndexAddr:
		log.Debugf("indexAddr %s", instr.X)
		f.recordRead(instr.X)
	case *ssa.FieldAddr:
		log.Debugf("fieldAddr %s", instr.X)
		f.recordRead(instr.X)
	}
}

func (f *fnSummary) visitStore(instr *ssa.Store) {
	log.Debugf("store %s %s", instr.Addr, f.fset.Position(instr.Addr.Pos()))
	f.recordWrite(instr.Addr)
}

func (f *fnSummary) visitUnOp(instr *ssa.UnOp) {
	switch instr.Op {
	case token.MUL:
		log.Debugf("deref %s %s", instr.X, f.fset.Position(instr.X.Pos()))
		f.recordRead(instr.X)
	case token.ARROW:
		log.Debugf("recv")
	}
}

func (f *fnSummary) visitMapUpdate(instr *ssa.MapUpdate) {
	log.Debugf("MapUpdate %s", instr.Map)
	f.recordWrite(instr.Map)
}

func (f *fnSummary) visitLookup(instr *ssa.Lookup)  {
	log.Debugf("Lookup %s", instr.X)
	f.recordRead(instr.X)
}

func (f *fnSummary) visitAlloc(instr *ssa.Alloc)  {
	log.Debugf("Alloc %s", instr)
	if instr.Heap {
		f.allocSet[instr] = instr
	}
}

func (f *fnSummary) visitCallCommon(instr ssa.CallCommon) {
	args := instr.Args
	if instr.Method != nil {
		args = append(args, instr.Value)
	}
}

func (f *fnSummary) recordRead(loc ssa.Value) {
	if pointer.CanPoint(loc.Type()) {
		f.accessSet[loc] = f.accessSet[loc]
	}
}

func (f *fnSummary) recordWrite(loc ssa.Value) {
	if pointer.CanPoint(loc.Type()) {
		f.accessSet[loc] = true
	}
}
