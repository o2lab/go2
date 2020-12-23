package analyzer

import (
	"github.com/o2lab/go2/pass"
	"github.com/o2lab/go2/summary"
	log "github.com/sirupsen/logrus"
	"go/token"
	"go/types"
	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/pointer"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"strings"
)

type AnalyzerConfig struct {
	Paths               []string
	ExcludedPackages    []string
	packages            []*ssa.Package
	program             *ssa.Program
	ptaResult           *pointer.Result
	sharedPtrSet        map[pointer.Pointer]bool
	fnSummaries         map[*ssa.Function]summary.FnSummary
	passes              map[*ssa.Function]*pass.FnPass
	accessesByAllocSite map[pointer.Pointer][]*pass.Access
	accessesMerged      map[pointer.Pointer][]*pass.Access
}

type Preprocessor struct {
	program     *ssa.Program
	ptaConfig   *pointer.Config
	excludedPkg map[string]bool
}

func NewAnalyzerConfig(paths []string, excluded []string) *AnalyzerConfig {
	return &AnalyzerConfig{
		Paths:               paths,
		ExcludedPackages:    excluded,
		program:             nil,
		packages:            nil,
		sharedPtrSet:        make(map[pointer.Pointer]bool),
		fnSummaries:         make(map[*ssa.Function]summary.FnSummary),
		passes:              make(map[*ssa.Function]*pass.FnPass),
		accessesByAllocSite: make(map[pointer.Pointer][]*pass.Access),
	}
}

func NewPreprocessor(prog *ssa.Program, ptaConfig *pointer.Config) *Preprocessor {
	return &Preprocessor{
		program:   prog,
		ptaConfig: ptaConfig,
	}
}

func (a *AnalyzerConfig) Run() {
	log.Infof("Loading packages %s", a.Paths)
	initial, err := packages.Load(&packages.Config{
		Mode:       packages.LoadAllSyntax,
		Context:    nil,
		Logf:       nil,
		Dir:        "",
		Env:        nil,
		BuildFlags: nil,
		Fset:       nil,
		ParseFile:  nil,
		Tests:      true,
		Overlay:    nil,
	}, a.Paths...)
	if err != nil {
		log.Fatalf("ERROR in loading packages: %s", err)
	}
	if packages.PrintErrors(initial) > 0 {
		log.Fatalln("ERROR in loading packages")
	}
	if len(initial) == 0 {
		log.Fatalln("ERROR: package list empty")
	}
	for _, pkg := range initial {
		log.Info(pkg.ID, pkg.GoFiles)
	}
	log.Infoln("Packages loaded. Building SSA...")
	a.program, _ = ssautil.AllPackages(initial, 0)
	a.program.Build()
	a.packages = a.program.AllPackages()

	log.Infof("SSA built for %d packages", len(a.packages))

	ptaConfig := &pointer.Config{
		Mains:           mainPackages(a.packages),
		Reflection:      false,
		BuildCallGraph:  true,
		Queries:         nil,
		IndirectQueries: nil,
		Log:             nil,
	}

	excludedPackages := make(map[string]bool)
	for _, pkgName := range a.ExcludedPackages {
		excludedPackages[pkgName] = true
	}
	preprocessor := NewPreprocessor(a.program, ptaConfig)
	a.fnSummaries = preprocessor.Run(a.packages, excludedPackages)

	log.Infoln("PTA")
	a.ptaResult, err = pointer.Analyze(ptaConfig)
	log.Infoln("PTA done")
	if err != nil {
		log.Fatalln(err)
	}

	domains := ComputeThreadDomains(a.ptaResult.CallGraph, excludedPackages, 3)
	funcAcquiredValues := make(map[*ssa.Function][]ssa.Value)
	err = GraphVisitEdgesFiltered(a.ptaResult.CallGraph, excludedPackages, func(edge *callgraph.Edge, stack pass.CallStack) error {
		callee := edge.Callee.Func
		// External function.
		if callee.Blocks == nil {
			return nil
		}

		log.Debugf("%s --> %s", edge.Caller.Func, edge.Callee.Func)
		//pass.PrintStack(stack)
		fnPass, ok := a.passes[callee]
		if !ok {
			fnPass = pass.NewFnPass(a.ptaResult, a.sharedPtrSet, a.accessesByAllocSite, domains[callee], a.fnSummaries[callee], funcAcquiredValues, stack)
			a.passes[callee] = fnPass
		}
		fnPass.Visit(callee)
		return nil
	})

	if err != nil {
		log.Fatalln(err)
	}

	for fun, acquiredValues := range funcAcquiredValues {
		if pass, ok := a.passes[fun]; ok {
			log.Debugf("Fun %s Acquires %+q", fun, acquiredValues)
			for _, accesses := range pass.Accesses {
				for _, acc := range accesses {
					acc.AcquiredValues = append(acc.AcquiredValues, acquiredValues...)
				}
			}
		}
	}

	if err != nil {
		log.Fatalln(err)
	}

	races := a.checkRaces()
	for _, race := range races {
		a.ReportRace(race)
	}
	log.Infof("Found %d race(s)", len(races))
}

func (a *AnalyzerConfig) ReportRace(race RacePair) {
	log.Println("========== DATA RACE ==========")
	log.Printf("  %s", race.First.StringWithPos(a.program.Fset))
	log.Println("  Call stack:")
	pass.PrintStack(race.First.Stack)
	log.Printf("  %s", race.Second.StringWithPos(a.program.Fset))
	log.Println("  Call stack:")
	pass.PrintStack(race.Second.Stack)
	log.Println("===============================")
}

// GraphVisitEdgesFiltered visits all reachable nodes from the root of g in post order. Nodes that belong to any package
// in excluded are not visited.
func GraphVisitEdgesFiltered(g *callgraph.Graph, excluded map[string]bool, edge func(*callgraph.Edge, pass.CallStack) error) error {
	seen := make(map[*callgraph.Node]bool)
	var visit func(n *callgraph.Node, stack pass.CallStack) error
	var stack pass.CallStack
	visit = func(n *callgraph.Node, stack pass.CallStack) error {
		if !seen[n] {
			seen[n] = true
			for _, e := range n.Out {
				callee := e.Callee
				if callee.Func.Pkg != nil {
					if pathRoot := strings.Split(callee.Func.Pkg.Pkg.Path(), "/")[0]; excluded[pathRoot] {
						return nil
					}
				}
				newStack := append(stack, e)
				if err := visit(callee, newStack); err != nil {
					return err
				}
				if err := edge(e, newStack); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(g.Root, stack); err != nil {
		return err
	}
	return nil
}

func ComputeThreadDomains(g *callgraph.Graph, excluded map[string]bool, times int) map[*ssa.Function]pass.ThreadDomain {
	seen := make(map[*callgraph.Node]int)
	var visit func(n *callgraph.Node, callerTid int)
	globalID := 0
	domains := make(map[*ssa.Function]pass.ThreadDomain)
	visit = func(n *callgraph.Node, callerTid int) {
		caller := n.Func
		if caller.Pkg != nil {
			if pathRoot := strings.Split(caller.Pkg.Pkg.Path(), "/")[0]; excluded[pathRoot] {
				return
			}
		}

		if seen[n] < times {
			seen[n] = seen[n] + 1
			if dom, ok := domains[caller]; ok {
				if callerTid != dom.ID {
					dom.Reflexive = true
				}
			} else {
				domains[caller] = pass.ThreadDomain{ID: callerTid}
			}
			for _, e := range n.Out {
				if e.Callee.Func.Blocks == nil {
					continue
				}
				if _, ok := e.Site.(*ssa.Go); ok {
					globalID++
				}
				visit(e.Callee, globalID)
			}
		}
		return
	}
	visit(g.Root, globalID)
	return domains
}

type RacePair struct {
	First, Second *pass.Access
}

func (a *AnalyzerConfig) checkRaces() (races []RacePair) {
	//mergedAccesses := a.filterAccesses()
	var reads, writes, allAcc []*pass.Access
	for _, accesses := range a.accessesByAllocSite {
		for _, acc := range accesses {
			if acc.Write {
				writes = append(writes, acc)
			} else {
				reads = append(reads, acc)
			}
		}
	}
	allAcc = append(allAcc, writes...)
	allAcc = append(allAcc, reads...)
	log.Infof("Check races for %d reads, %d writes", len(reads), len(writes))
	for i := 0; i < len(writes); i++ {
		for j := i + 1; j < len(allAcc); j++ {
			log.Debugf("Check %s <> %s", writes[i], allAcc[j])
			if a.checkRace(writes[i], allAcc[j]) {
				races = append(races, RacePair{writes[i], allAcc[j]})
			}
		}
	}
	return
}

func (a *AnalyzerConfig) checkRace(acc1, acc2 *pass.Access) bool {
	if !acc1.WriteAndThreadConflictsWith(acc2) ||
		acc1.MutualExclusive(acc2, a.ptaResult.Queries) ||
		!acc1.MayAlias(acc2, a.ptaResult.Queries) {
		return false
	}
	return true
}

func (a *AnalyzerConfig) MayAlias(x, y ssa.Value) bool {
	return a.ptaResult.Queries[x].MayAlias(a.ptaResult.Queries[y])
}

func (a *AnalyzerConfig) filterAccesses() map[pointer.Pointer][]*pass.Access {
	result := make(map[pointer.Pointer][]*pass.Access)
	for ptr1, _ := range a.sharedPtrSet {
		for ptr2, accesses := range a.accessesByAllocSite {
			if ptr1.MayAlias(ptr2) {
				result[ptr1] = append(result[ptr1], accesses...)
			}
		}
	}
	return result
}

func mainPackages(pkgs []*ssa.Package) []*ssa.Package {
	var mains []*ssa.Package
	for _, p := range pkgs {
		if p != nil && p.Pkg.Name() == "main" && p.Func("main") != nil {
			mains = append(mains, p)
		}
	}
	if len(mains) == 0 {
		log.Fatalf("no main packages")
	}
	return mains
}

func (p *Preprocessor) Run(packages []*ssa.Package, excludedPackages map[string]bool) map[*ssa.Function]summary.FnSummary {
	log.Debugln("Preprocessing...")
	p.excludedPkg = excludedPackages
	summaries := make(map[*ssa.Function]summary.FnSummary)
	for _, pkg := range packages {
		log.Infof("Preprocessing %s", pkg)
		if p.excludedPkg[pkg.Pkg.Name()] {
			log.Debugf("Exclude pkg %s", pkg)
			continue
		}
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

func (p *Preprocessor) visitFunction(function *ssa.Function) summary.FnSummary {
	if p.excludedPkg[function.Pkg.Pkg.Name()] {
		log.Debugln("Exclude", function)
	}
	log.Debugf("visiting %s: %s", function, function.Type())

	// Skip external functions.
	if function.Blocks == nil {
		return summary.FnSummary{}
	}

	//for _, param := range function.Params {
	//	p.lookupPos(int(param.Pos()))
	//}

	sum := summary.NewFnSummary(p.program.Fset)
	sum.Summarize(function, p.ptaConfig)

	for _, anonFn := range function.AnonFuncs {
		p.visitFunction(anonFn)
	}
	return *sum
}

func (p *Preprocessor) lookupPos(pos int) {
	log.Infoln(p.program.Fset.Position(token.Pos(pos)))
}
