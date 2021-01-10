package preprocessor

import (
	"github.com/o2lab/go2/pointer"
	"github.com/sirupsen/logrus"
	"go/token"
	"go/types"
	"golang.org/x/tools/go/ssa"
)

type Preprocessor struct {
	program       *ssa.Program
	ptaConfig     *pointer.Config
	ExcludedPkg   map[string]bool
	EscapedValues map[*ssa.Go][]ssa.Value
}

func NewPreprocessor(prog *ssa.Program, ptaConfig *pointer.Config, excluded []string) *Preprocessor {
	excludedPkg := make(map[string]bool)
	for _, pkg := range excluded {
		excludedPkg[pkg] = true
	}
	return &Preprocessor{
		program:       prog,
		ptaConfig:     ptaConfig,
		ExcludedPkg:   excludedPkg,
		EscapedValues: make(map[*ssa.Go][]ssa.Value),
	}
}

func (p *Preprocessor) Run(packages []*ssa.Package) map[*ssa.Function]FnSummary {
	logrus.Debugln("Preprocessing...")
	summaries := make(map[*ssa.Function]FnSummary)
	for _, pkg := range packages {
		if p.ExcludedPkg[pkg.Pkg.Name()] {
			logrus.Debugf("Exclude pkg %s", pkg)
			continue
		}
		logrus.Debugf("Preprocessing %s", pkg)
		for _, member := range pkg.Members {
			if function, ok := member.(*ssa.Function); ok {
				summaries[function] = p.visitFunction(function)
			} else if typ, ok := member.(*ssa.Type); ok {
				// For a named struct, we visit all its functions.
				goType := typ.Type()
				if namedType, ok := goType.(*types.Named); ok {
					for i := 0; i < namedType.NumMethods(); i++ {
						method := namedType.Method(i)
						function := p.program.FuncValue(method)
						summaries[function] = p.visitFunction(function)
					}
				}
			} else if global, ok := member.(*ssa.Global); ok {
				p.ptaConfig.AddQuery(global)
			}
		}
	}
	return summaries
}

func (p *Preprocessor) visitFunction(function *ssa.Function) FnSummary {
	if p.ExcludedPkg[function.Pkg.Pkg.Name()] {
		logrus.Debugln("Exclude", function)
	}
	logrus.Debugf("visiting %s: %s", function, function.Type())

	// Skip external functions.
	if function.Blocks == nil {
		return FnSummary{}
	}

	//for _, param := range function.Params {
	//	p.lookupPos(int(param.Pos()))
	//}

	sum := NewFnSummary(p.program.Fset, p)
	sum.Preprocess(function, p.ptaConfig)

	for _, anonFn := range function.AnonFuncs {
		p.visitFunction(anonFn)
	}
	return *sum
}

func (p *Preprocessor) lookupPos(pos int) {
	logrus.Infoln(p.program.Fset.Position(token.Pos(pos)))
}
