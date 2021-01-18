package analyzer

import (
	"github.com/o2lab/go2/pass"
	"github.com/o2lab/go2/pointer"
	"github.com/o2lab/go2/preprocessor"
	log "github.com/sirupsen/logrus"
	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/packages"
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
	fnSummaries         map[*ssa.Function]preprocessor.FnSummary
	passes              map[*ssa.Function]*pass.FnPass
	accessesByAllocSite map[pointer.Pointer][]*pass.Access
	accessesMerged      map[pointer.Pointer][]*pass.Access
}

func NewAnalyzerConfig(paths []string, excluded []string) *AnalyzerConfig {
	return &AnalyzerConfig{
		Paths:               paths,
		ExcludedPackages:    excluded,
		program:             nil,
		packages:            nil,
		sharedPtrSet:        make(map[pointer.Pointer]bool),
		fnSummaries:         make(map[*ssa.Function]preprocessor.FnSummary),
		passes:              make(map[*ssa.Function]*pass.FnPass),
		accessesByAllocSite: make(map[pointer.Pointer][]*pass.Access),
	}
}

func (a *AnalyzerConfig) Run() {
	log.Infof("Loading packages at %s", a.Paths)
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

	preprocessor := preprocessor.NewPreprocessor(a.program, ptaConfig, a.ExcludedPackages)
	a.fnSummaries = preprocessor.Run(a.packages)

	log.Infoln("PTA")
	a.ptaResult, err = pointer.Analyze(ptaConfig)
	log.Infoln("PTA done")
	if err != nil {
		log.Fatalln(err)
	}

	instrEdgeMap := make(map[ssa.CallInstruction][]*callgraph.Edge)
	cfgVisitor := pass.NewCFGVisitorState(a.ptaResult, a.sharedPtrSet, preprocessor.EscapedValues, a.program, instrEdgeMap)
	err = GraphVisitEdgesFiltered(a.ptaResult.CallGraph, preprocessor.ExcludedPkg, func(edge *callgraph.Edge, stack pass.CallStack) error {
		callee := edge.Callee.Func
		// External function.
		if callee.Blocks == nil {
			return nil
		}
		instrEdgeMap[edge.Site] = append(instrEdgeMap[edge.Site], edge)
		// Temporarily ignore functions in the testing package.
		if callee.Pkg != nil && strings.Split(callee.Pkg.Pkg.Path(), "/")[0] != "testing" {
			log.Infof("%s --> %s", edge.Caller.Func, edge.Callee.Func)
			cfgVisitor.VisitFunction(callee, stack)
		}
		return nil
	})

	if err != nil {
		log.Fatalln(err)
	}
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
						continue
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
