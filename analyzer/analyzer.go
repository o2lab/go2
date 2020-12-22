package analyzer

import (
	"github.com/o2lab/go2/pass"
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
	passes              map[*ssa.Function]*pass.FnPass
	accessesByAllocSite map[pointer.Pointer][]*pass.Access
	accessesMerged      map[pointer.Pointer][]*pass.Access
}

type Preprocessor struct {
	fnSummaries map[*ssa.Function]fnSummary
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
		passes:              make(map[*ssa.Function]*pass.FnPass),
		accessesByAllocSite: make(map[pointer.Pointer][]*pass.Access),
	}
}

func NewPreprocessor(prog *ssa.Program, ptaConfig *pointer.Config) *Preprocessor {
	return &Preprocessor{
		fnSummaries: make(map[*ssa.Function]fnSummary),
		program:     prog,
		ptaConfig:   ptaConfig,
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
		Tests:      false,
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
	preprocessor.Run(a.packages, excludedPackages)

	log.Infoln("PTA")
	a.ptaResult, err = pointer.Analyze(ptaConfig)
	log.Infoln("PTA done")
	if err != nil {
		log.Fatalln(err)
	}

	err = GraphVisitEdgesFilteredPreorder(a.ptaResult.CallGraph, excludedPackages, 3, func(edge *callgraph.Edge, threadDomain pass.ThreadDomain) error {
		callee := edge.Callee.Func
		// External function.
		if callee.Blocks == nil {
			return nil
		}
		fnPass, ok := a.passes[callee]
		if !ok {
			fnPass = pass.NewFnPass(a.ptaResult, a.sharedPtrSet, a.accessesByAllocSite, threadDomain)
			a.passes[callee] = fnPass
		} else if threadDomain == pass.Parallel {
			fnPass.LiftThreadDomain()
		}
		return nil
	})

	err = GraphVisitEdgesFiltered(a.ptaResult.CallGraph, excludedPackages, func(edge *callgraph.Edge) error {
		callee := edge.Callee.Func
		// External function.
		if callee.Blocks == nil {
			return nil
		}

		log.Infof("%s --> %s", edge.Caller.Func, edge.Callee.Func)
		fnPass, ok := a.passes[callee]
		if !ok {
			fnPass = pass.NewFnPass(a.ptaResult, a.sharedPtrSet, a.accessesByAllocSite, pass.MainThreadOnly)
			a.passes[callee] = fnPass
		}
		fnPass.Visit(callee)
		return nil
	})

	if err != nil {
		log.Fatalln(err)
	}

	races := a.checkRaces()
	for _, race := range races {
		a.ReportRace(race)
	}
}

func (a *AnalyzerConfig) ReportRace(race RacePair) {
	log.Println("========== DATA RACE ==========")
	log.Printf("    %s", race.First.StringWithPos(a.program.Fset))
	log.Printf("    %s", race.Second.StringWithPos(a.program.Fset))
	log.Println("===============================")
}

// GraphVisitEdgesFiltered visits all reachable nodes from the root of g in post order. Nodes that belong to any package
// in excluded are not visited.
func GraphVisitEdgesFiltered(g *callgraph.Graph, excluded map[string]bool, edge func(*callgraph.Edge) error) error {
	seen := make(map[*callgraph.Node]bool)
	var visit func(n *callgraph.Node) error
	visit = func(n *callgraph.Node) error {
		if !seen[n] {
			seen[n] = true
			for _, e := range n.Out {
				callee := e.Callee
				if callee.Func.Pkg != nil {
					if pathRoot := strings.Split(callee.Func.Pkg.Pkg.Path(), "/")[0]; excluded[pathRoot] {
						return nil
					}
				}
				if err := visit(callee); err != nil {
					return err
				}
				if err := edge(e); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(g.Root); err != nil {
		return err
	}
	return nil
}

func GraphVisitEdgesFilteredPreorder(g *callgraph.Graph, excluded map[string]bool, times int, edge func(*callgraph.Edge, pass.ThreadDomain) error) error {
	seen := make(map[*callgraph.Node]int)
	var visit func(n *callgraph.Node, domain pass.ThreadDomain) error
	visit = func(n *callgraph.Node, callerThread pass.ThreadDomain) error {
		if seen[n] < times {
			seen[n] = seen[n] + 1
			for _, e := range n.Out {
				callee := e.Callee
				if callee.Func.Pkg != nil {
					if pathRoot := strings.Split(callee.Func.Pkg.Pkg.Path(), "/")[0]; excluded[pathRoot] {
						return nil
					}
				}
				if callee.Func.Blocks == nil {
					return nil
				}
				calleeThread := callerThread
				if _, ok := e.Site.(*ssa.Go); ok {
					calleeThread = pass.Parallel
				}
				if err := edge(e, calleeThread); err != nil {
					return err
				}
				if err := visit(callee, calleeThread); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(g.Root, pass.MainThreadOnly); err != nil {
		return err
	}
	return nil
}

type RacePair struct {
	First, Second *pass.Access
}

func (a *AnalyzerConfig) checkRaces() (races []RacePair) {
	log.Infoln("Check races")
	//a.accessesMerged = a.filterAccesses()
	ptrSet := make([]pointer.Pointer, len(a.accessesByAllocSite))
	mergedAccesses := make(map[pointer.Pointer][]*pass.Access)
	merged := make(map[pointer.Pointer]bool, len(a.accessesByAllocSite))
	i := 0
	for k := range a.accessesByAllocSite {
		ptrSet[i] = k
		i++
	}
	for j := 0; j < len(ptrSet); j++ {
		for k := j + 1; k < len(ptrSet); k++ {
			p1, p2 := ptrSet[j], ptrSet[k]
			if merged[p1] {
				continue
			}
			if p1.MayAlias(p2) {
				mergedAccesses[p1] = append(mergedAccesses[p1], a.accessesByAllocSite[p1]...)
				mergedAccesses[p1] = append(mergedAccesses[p1], a.accessesByAllocSite[p2]...)
				merged[p2] = true
			}
		}
	}
	for _, accesses := range mergedAccesses {
		for i := 0; i < len(accesses); i++ {
			for j := i; j < len(accesses); j++ {
				log.Infof("Check %s <> %s", accesses[i], accesses[j])
				if a.checkRace(accesses[i], accesses[j]) {
					races = append(races, RacePair{accesses[i], accesses[j]})
				}
			}
		}
	}
	return
}

func (a *AnalyzerConfig) checkRace(acc1, acc2 *pass.Access) bool {
	if !acc1.WriteAndThreadConflictsWith(acc2) || acc1.MutualExclusive(acc2, a.ptaResult.Queries) {
		return false
	}
	return true
}

func (a *AnalyzerConfig) MayAlias(x, y ssa.Value) bool {
	return a.ptaResult.Queries[x].MayAlias(a.ptaResult.Queries[y])
}

func (a *AnalyzerConfig) filterAccesses() map[pointer.Pointer][]*pass.Access {
	result := make(map[pointer.Pointer][]*pass.Access)
	//for ptr, _ := range a.accessesByAllocSite {
	//
	//}
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

func (p *Preprocessor) Run(packages []*ssa.Package, excludedPackages map[string]bool) {
	log.Debugln("Preprocessing...")
	p.excludedPkg = excludedPackages
	for _, pkg := range packages {
		log.Infof("Preprocessing %s", pkg)
		if p.excludedPkg[pkg.Pkg.Name()] {
			log.Debugf("Exclude pkg %s", pkg)
			continue
		}
		for _, member := range pkg.Members {
			if function, ok := member.(*ssa.Function); ok {
				p.visitFunction(function)
			} else if typ, ok := member.(*ssa.Type); ok {
				// For a named struct, we visit all its functions.
				goType := typ.Type()
				if namedType, ok := goType.(*types.Named); ok {
					for i := 0; i < namedType.NumMethods(); i++ {
						method := namedType.Method(i)
						p.visitFunction(p.program.FuncValue(method))
					}
				}
			} else if global, ok := member.(*ssa.Global); ok {
				p.ptaConfig.AddQuery(global)
			}
		}
	}
}

func (p *Preprocessor) visitFunction(function *ssa.Function) {
	if p.excludedPkg[function.Pkg.Pkg.Name()] {
		log.Debugln("Exclude", function)
	}
	log.Debugf("visiting %s: %s", function, function.Type())

	// Skip external functions.
	if function.Blocks == nil {
		return
	}

	if _, ok := p.fnSummaries[function]; ok {
		log.Fatalf("Already visited %s", function)
	}
	//for _, param := range function.Params {
	//	p.lookupPos(int(param.Pos()))
	//}

	sum := fnSummary{
		fset:      p.program.Fset,
		accessSet: make(map[ssa.Value]IsWrite),
		allocSet:  make(map[*ssa.Alloc]ssa.Instruction),
	}
	sum.summarize(function, p.ptaConfig)

	for _, anonFn := range function.AnonFuncs {
		p.visitFunction(anonFn)
	}
}

func (p *Preprocessor) lookupPos(pos int) {
	log.Infoln(p.program.Fset.Position(token.Pos(pos)))
}
