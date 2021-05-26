package main

import (
	"fmt"
	"github.com/april1989/origin-go-tools/go/pointer"
	pta0 "github.com/april1989/origin-go-tools/go/pointer_default"
	"github.com/april1989/origin-go-tools/go/ssa"
	log "github.com/sirupsen/logrus"
	"github.com/twmb/algoimpl/go/graph"
	"strconv"
	"strings"
	"sync"
)

type analysis struct {
	mu      sync.RWMutex
	ptaRes  *pointer.Result //now can reuse the ptaRes
	ptaRes0 *pta0.Result
	ptaCfg  *pointer.Config
	ptaCfg0 *pta0.Config

	efficiency bool
	trieLimit  int
	//getGo        bool // flag
	prog            *ssa.Program
	main            *ssa.Package
	analysisStat    stat
	HBgraph         *graph.Graph
	RWinsMap        map[goIns]graph.Node
	trieMap         map[fnInfo]*trie // map each function to a trie node -> bz: now it includes all traversed fns
	RWIns           [][]*insInfo     // instructions grouped by goroutine
	insMono         int              // index of instruction (in main goroutine) before which the program is single-threaded
	storeFns        []*fnCallInfo
	workList        []goroutineInfo
	levels          map[int]int
	lockMap         map[ssa.Instruction][]ssa.Value // map each read/write access to a snapshot of actively maintained lockset
	lockSet         map[int][]*lockInfo             // active lockset, to be maintained along instruction traversal
	RlockMap        map[ssa.Instruction][]ssa.Value // map each read/write access to a snapshot of actively maintained lockset
	RlockSet        map[int][]*lockInfo             // active lockset, to be maintained along instruction traversal
	getParam        bool
	paramFunc       *ssa.Function
	goStack         [][]*fnCallInfo
	goCaller        map[int]int
	goCalls         map[int]*goCallInfo
	chanToken       map[string]string      // map token number to channel name
	chanBuf         map[string]int         // map each channel to its buffer length
	chanRcvs        map[string][]*ssa.UnOp // map each channel to receive instructions
	chanSnds        map[string][]*ssa.Send // map each channel to send instructions
	chanName        string
	selectBloc      map[int]*ssa.Select             // index of block where select statement was encountered
	selReady        map[*ssa.Select][]string        // store name of ready channels for each select statement
	selUnknown      map[*ssa.Select][]string        // channels are passed in as parameters
	selectCaseBegin map[ssa.Instruction]string      // map first instruction in clause to channel name
	selectCaseEnd   map[ssa.Instruction]string      // map last instruction in clause to channel name
	selectCaseBody  map[ssa.Instruction]*ssa.Select // map instructions to select instruction
	selectDone      map[ssa.Instruction]*ssa.Select // map first instruction after select is done to select statement
	ifSuccBegin     map[ssa.Instruction]*ssa.If     // map beginning of succ block to if statement
	ifFnReturn      map[*ssa.Function]*ssa.Return   // map "if-containing" function to its final return
	ifSuccEnd       map[ssa.Instruction]*ssa.Return // map ending of successor block to final return statement
	commIfSucc      []ssa.Instruction               // store first ins of succ block that contains channel communication
	omitComm        []*ssa.BasicBlock               // omit these blocks as they are race-free due to channel communication
	racyStackTops   []string
	inLoop          bool // entered a loop
	goInLoop        map[int]bool
	loopIDs         map[int]int // map goID to loopID
	allocLoop       map[*ssa.Function][]string
	bindingFV       map[*ssa.Go][]*ssa.FreeVar
	//pbr             *ssa.Alloc //bz: this is not used
	commIDs    map[int][]int
	deferToRet map[*ssa.Defer]ssa.Instruction

	entryFn    string          //bz: move from global to analysis field
	testEntry  *ssa.Function   //bz: test entry point -> just one!
	otherTests []*ssa.Function //bz: all other tests that are in the same test pkg, TODO: bz: exclude myself

	twinGoID map[*ssa.Go][]int //bz: whether two goroutines are spawned by the same loop; this might not be useful now since !sliceContains(a.reportedAddr, addressPair) && already filtered out the duplicate race check
	//mutualTargets   map[int]*mutualFns //bz: this mutual exclusion is for this specific go id (i.e., int)
}

// fromPkgsOfInterest determines if a function is from a package of interest
func (a *analysis) fromPkgsOfInterest(fn *ssa.Function) bool {
	if fn.Pkg == nil || fn.Pkg.Pkg == nil {
		if fn.IsFromApp { //bz: do not remove this ... otherwise will miss racy functions
			return true
		}
		return false
	}
	if fn.Pkg.Pkg.Name() == "main" || fn.Pkg.Pkg.Name() == "cli" || fn.Pkg.Pkg.Name() == "testing" {
		return true
	}
	for _, excluded := range excludedPkgs {
		if fn.Pkg.Pkg.Name() == excluded || fn.Pkg.Pkg.Path() == excluded { //bz: some lib's Pkg.Name() == "Package", not the used import xxx; if so, check Pkg.Path()
			return false
		}
	}
	if a.efficiency && a.main.Pkg.Path() != "command-line-arguments" && !strings.HasPrefix(fn.Pkg.Pkg.Path(), strings.Split(a.main.Pkg.Path(), "/")[0]) { // path is dependent on tested program
		return false
	}
	return true
}

func (a *analysis) fromExcludedFns(fn *ssa.Function) bool {
	strFn := fn.String()
	for _, ex := range excludedFns {
		if strings.HasPrefix(strFn, ex) {
			return true
		}
	}
	return false
}

//bz: do not want to see this big block ...
func initialAnalysis() *analysis {
	return &analysis{
		efficiency:      efficiency,
		trieLimit:       trieLimit,
		RWinsMap:        make(map[goIns]graph.Node),
		trieMap:         make(map[fnInfo]*trie),
		insMono:         -1,
		levels:          make(map[int]int),
		lockMap:         make(map[ssa.Instruction][]ssa.Value),
		lockSet:         make(map[int][]*lockInfo),
		RlockMap:        make(map[ssa.Instruction][]ssa.Value),
		RlockSet:        make(map[int][]*lockInfo),
		goCaller:        make(map[int]int),
		goCalls:         make(map[int]*goCallInfo),
		chanToken:       make(map[string]string),
		chanBuf:         make(map[string]int),
		chanRcvs:        make(map[string][]*ssa.UnOp),
		chanSnds:        make(map[string][]*ssa.Send),
		selectBloc:      make(map[int]*ssa.Select),
		selReady:        make(map[*ssa.Select][]string),
		selUnknown:      make(map[*ssa.Select][]string),
		selectCaseBegin: make(map[ssa.Instruction]string),
		selectCaseEnd:   make(map[ssa.Instruction]string),
		selectCaseBody:  make(map[ssa.Instruction]*ssa.Select),
		selectDone:      make(map[ssa.Instruction]*ssa.Select),
		ifSuccBegin:     make(map[ssa.Instruction]*ssa.If),
		ifFnReturn:      make(map[*ssa.Function]*ssa.Return),
		ifSuccEnd:       make(map[ssa.Instruction]*ssa.Return),
		inLoop:          false,
		goInLoop:        make(map[int]bool),
		loopIDs:         make(map[int]int),
		allocLoop:       make(map[*ssa.Function][]string),
		bindingFV:       make(map[*ssa.Go][]*ssa.FreeVar),
		commIDs:         make(map[int][]int),
		deferToRet:      make(map[*ssa.Defer]ssa.Instruction),
		twinGoID:        make(map[*ssa.Go][]int),
		//mutualTargets:   make(map[int]*mutualFns),
	}
}

//bz: abstract out
func (a *analysis) runChecker(multiSamePkgs bool) raceReport {
	if strings.Contains(a.main.Pkg.Path(), "GoBench") { // for testing purposes
		a.efficiency = false
		a.trieLimit = 2
	} else if !goTest {
		a.efficiency = true
	}
	if DEBUG {
		log.Info("Compiling stack trace for every Goroutine... ")
		log.Debug(strings.Repeat("-", 35), "Stack trace begins", strings.Repeat("-", 35))
	}
	if a.testEntry != nil {
		//bz: a test now uses itself as main context, tell pta which test will be analyzed for this analysis
		a.ptaRes.AnalyzeTest(a.testEntry)
		a.traverseFn(a.testEntry, a.testEntry.Name(), 0, nil)
	} else {
		mainFn := a.main.Func(a.entryFn)
		a.traverseFn(mainFn, mainFn.Name(), 0, nil)
	}

	if DEBUG {
		log.Debug(strings.Repeat("-", 35), "Stack trace ends", strings.Repeat("-", 35))
	}
	totalIns := 0
	for g := range a.RWIns {
		totalIns += len(a.RWIns[g])
	}
	traversed := make(map[*ssa.Function]*ssa.Function)
	for fn, _ := range a.trieMap { //bz: remove diff context for the same fn
		traversed[fn.fnName] = fn.fnName
	}
	if DEBUG { //bz: debug
		for _, fn := range traversed {
			fmt.Println(fn.String())
		}
	}

	doEndLog("Done  -- " + strconv.Itoa(len(a.RWIns)) + " goroutines analyzed! " + strconv.Itoa(len(traversed)) + " function traversed! " + strconv.Itoa(totalIns) + " instructions of interest detected! ")

	if len(a.RWIns) == 1 { //bz: only main thread, no races.
		log.Info("Only has the main goroutine, no need to continue. Return. ")
		return a.getRaceReport(false)
	}

	if useDefaultPTA {
		a.ptaRes0, _ = pta0.Analyze(a.ptaCfg0) // all queries have been added, conduct pointer analysis
	}
	//if !allEntries {
	doStartLog("Building Happens-Before graph... ")
	//}
	// confirm channel readiness for unknown select cases:
	if len(a.selUnknown) > 0 {
		for sel, chs := range a.selUnknown {
			for i, ch := range chs {
				if _, ready := a.chanSnds[ch]; !ready && ch != "" {
					if _, ready0 := a.chanRcvs[ch]; !ready0 {
						if _, ready1 := a.chanBuf[a.chanToken[ch]]; !ready1 {
							//bz: here has error:
							//INFO[20:50:17] Traversing Statements for test entry point: github.com/ethereum/go-ethereum/contracts/checkpointoracle.TestCheckpointRegister...
							//panic: runtime error: index out of range [2] with length 2
							exist := a.selReady[sel]
							if i < len(exist) {
								a.selReady[sel][i] = ""
							}
						}
					}
				}
			}
		}
	}
	a.HBgraph = graph.New(graph.Directed)
	a.buildHB()
	//if !allEntries {
	doEndLog("Done  -- Happens-Before graph built ")
	log.Info("Checking for data races... ") //bz: no spinner -> we need to print out ...
	//}
	rr := a.getRaceReport(multiSamePkgs)
	rr.racePairs = a.checkRacyPairs()

	return rr
}

//bz:
func (a *analysis) getRaceReport(multiSamePkgs bool) raceReport {
	entryStr := a.main.Pkg.Path()
	if a.testEntry != nil {
		entryStr = a.testEntry.String() //duplicate name in summary
	}
	if multiSamePkgs {
		pkg := a.main.Pkg
		if pkg.Path() != "command-line-arguments" {
			entryStr = pkg.String()
		}else{
			loc := pkg.Scope().Child(0).String() //bz: copied from util.go
			idx := strings.Index(loc, ".go")
			loc = loc[:idx+3]
			entryStr = entryStr + "(" + loc + ")"
		}
	}
	rr := raceReport{
		entryInfo: entryStr,
	}
	return rr
}

// visitAllInstructions visits each line and calls the corresponding helper function to drive the tool
func (a *analysis) visitAllInstructions(fn *ssa.Function, goID int) {
	lockSetSize := len(a.lockSet[goID])
	//a.analysisStat.nGoroutine = goID + 1 // keep count of goroutine quantity
	if fn == nil {
		return
	}
	if !isSynthetic(fn) { // if function is NOT synthetic
		if !a.fromPkgsOfInterest(fn) {
			a.updateRecords(fn.Name(), goID, "POP  ", fn, nil)
			a.visitGo()
			return
		}
		if fn.Name() == a.entryFn { //bz: for main only
			if goID == 0 && len(a.storeFns) == 1 {
				a.loopIDs[goID] = 0
				a.goStack = append(a.goStack, []*fnCallInfo{}) // initialize first interior slice for main goroutine
			} else { //revisiting entry-point
				return
			}
		}
	}
	if _, ok := a.levels[goID]; !ok && goID > 0 { // initialize level counter for new goroutine
		a.levels[goID] = 1
	}
	if goID >= len(a.RWIns) { // initialize interior slice for new goroutine
		a.RWIns = append(a.RWIns, []*insInfo{})
	}

	bVisit0 := fn.DomPreorder()
	var bVisit []*ssa.BasicBlock
	var pushBack []*ssa.BasicBlock // stack of .done blocks
	statement := ""                // could be if, for or rangeiter
	for i, b := range bVisit0 {
		if len(pushBack) > 0 && !strings.Contains(b.Comment, statement) { // reach end of statement blocks
			bVisit = append(bVisit, pushBack...) // LIFO
			pushBack = []*ssa.BasicBlock{}       // empty stack
			statement = ""                       // reinitialize
		}
		if strings.Contains(b.Comment, ".done") && i < len(bVisit0)-1 { // not the last block
			statement = strings.Split(b.Comment, ".done")[0]
			pushBack = append(pushBack, b)
		} else {
			bVisit = append(bVisit, b)
		}
		if len(pushBack) > 0 && i == len(bVisit0)-1 { // reach end of statement blocks
			bVisit = append(bVisit, pushBack...) // LIFO
			pushBack = []*ssa.BasicBlock{}       // empty stack
		}
	}

	var toDefer []ssa.Instruction // stack storing deferred calls
	var toUnlock []ssa.Value
	var toRUnlock []ssa.Value
	var readyChans []string
	var selIns *ssa.Select // current select statement
	var selCount int       // total cases in a select statement
	var activeCase bool
	var defCase bool
	var selDone bool
	var ifIns *ssa.If
	var ifEnds []ssa.Instruction
	for bInd, aBlock := range bVisit {
		activeCase, selDone = false, false
		if aBlock.Comment == "recover" { // ----> !!! SEE HERE: bz: the same as above, from line 279 to 293 (or even 304) can be separated out
			continue
		}
		if aBlock.Comment == "select.done" {
			a.selectDone[aBlock.Instrs[0]] = selIns // map first ins in select.done to select instruction
			selDone = true
		}
		if aBlock.Comment == "select.body" && selCount < len(readyChans) {
			if readyChans[selCount] == "" {
				selCount++
				continue // skip unready case
			} else {
				activeCase = true
			}
		}
		if selIns != nil && aBlock.Comment == "select.next" && !selIns.Blocking && readyChans[selCount] == "defaultCase" {
			a.selectCaseBegin[aBlock.Instrs[0]] = readyChans[selCount]                  // map first instruction in case to channel name
			a.selectCaseEnd[aBlock.Instrs[len(aBlock.Instrs)-1]] = readyChans[selCount] // map last instruction in case to channel name
			defCase = true
		}
		if ifIns != nil && (aBlock.Comment == "if.then" || aBlock.Comment == "if.else" || aBlock.Comment == "if.done") {
			a.ifSuccBegin[aBlock.Instrs[0]] = ifIns
			if len(aBlock.Succs) == 0 {
				ifEnds = append(ifEnds, aBlock.Instrs[len(aBlock.Instrs)-1])
			}
		}
		if aBlock.Comment == "for.body" || aBlock.Comment == "rangeindex.body" || aBlock.Comment == "rangeiter.body" {
			a.inLoop = true // TODO: consider nested loops
		}
		for ii, theIns := range aBlock.Instrs { // examine each instruction
			if theIns.String() == "rundefers" { // execute deferred calls at this index
				a.recordIns(goID, theIns)
				for _, dIns := range toDefer { // ----> !!! SEE HERE: bz: the same as above, from line 307 to 347 can be separated out
					deferIns := dIns.(*ssa.Defer)
					a.deferToRet[deferIns] = theIns
					if _, ok := deferIns.Call.Value.(*ssa.Builtin); ok {
						continue
					}
					if deferIns.Call.StaticCallee() == nil {
						continue
					} else if a.fromPkgsOfInterest(deferIns.Call.StaticCallee()) && deferIns.Call.StaticCallee().Pkg.Pkg.Name() != "sync" {
						fnName := deferIns.Call.Value.Name()
						fnName = checkTokenNameDefer(fnName, deferIns)
						a.traverseFn(deferIns.Call.StaticCallee(), fnName, goID, dIns)
					} else if deferIns.Call.StaticCallee().Name() == "Unlock" {
						lockLoc := deferIns.Call.Args[0]
						if !useNewPTA {
							a.mu.Lock()
							a.ptaCfg0.AddQuery(lockLoc)
							a.mu.Unlock()
						}
						toUnlock = append(toUnlock, lockLoc)
					} else if deferIns.Call.StaticCallee().Name() == "RUnlock" {
						RlockLoc := deferIns.Call.Args[0]
						if !useNewPTA {
							a.mu.Lock()
							a.ptaCfg0.AddQuery(RlockLoc)
							a.mu.Unlock()
						}
						toRUnlock = append(toRUnlock, RlockLoc)
					} else if deferIns.Call.Value.Name() == "Done" {
						a.recordIns(goID, dIns)
						if !useNewPTA {
							a.mu.Lock()
							a.ptaCfg0.AddQuery(deferIns.Call.Args[0])
							a.mu.Unlock()
						}
					}
				}
				toDefer = []ssa.Instruction{}
			}
			for _, ex := range excludedPkgs { // TODO: need revision
				if !isSynthetic(fn) && ex == theIns.Parent().Pkg.Pkg.Name() {
					return
				}
			}
			switch examIns := theIns.(type) {
			case *ssa.MakeChan: // channel creation op
				a.insMakeChan(examIns, ii)
			case *ssa.Send: // channel send op
				chNm := a.insSend(examIns, goID, theIns)
				isAwait := false // is the channel send being awaited on by select?
				for _, chs := range a.selReady {
					if sliceContainsStr(chs, chNm) {
						isAwait = true
						break
					}
				}
				if isAwait && (examIns.Block().Comment == "if.then" || examIns.Block().Comment == "if.else" || examIns.Block().Comment == "if.done") {
					// send awaited on by select, other if successor will not be traversed
					a.commIfSucc = append(a.commIfSucc, examIns.Block().Instrs[0])
				}
			case *ssa.Store: // write op
				if _, ok := examIns.Addr.(*ssa.Alloc); ok && ii > 0 { // variable initialization
					switch aBlock.Instrs[ii-1].(type) {
					case *ssa.Alloc:
					case *ssa.MakeChan: // channel object
					case *ssa.Extract: // tuple index
						if ii < 2 {
							a.insStore(examIns, goID, theIns)
						} else {
							if _, ok1 := aBlock.Instrs[ii-2].(*ssa.Alloc); !ok1 {
								a.insStore(examIns, goID, theIns)
							}
						}
					default:
						if _, ok1 := examIns.Val.(*ssa.Alloc); ok1 /*&& v.Comment == "complit"*/ {
							// declare&init
						} else {
							a.insStore(examIns, goID, theIns)
						}
					}
				} else {
					a.insStore(examIns, goID, theIns)
				}
			case *ssa.UnOp:
				a.insUnOp(examIns, goID, theIns)
			case *ssa.FieldAddr:
				a.insFieldAddr(examIns, goID, theIns)
			case *ssa.Lookup: // look up element index, read op
				a.insLookUp(examIns, goID, theIns)
			case *ssa.ChangeType: // a value-preserving type change, write op
				a.insChangeType(examIns, goID, theIns)
			case *ssa.Defer:
				toDefer = append([]ssa.Instruction{theIns}, toDefer...)
			case *ssa.MakeInterface: // construct instance of interface type
				a.insMakeInterface(examIns, goID, theIns)
			case *ssa.Call:
				unlockOps, runlockOps := a.insCall(examIns, goID, theIns)
				toUnlock = append(toUnlock, unlockOps...)
				toRUnlock = append(toRUnlock, runlockOps...)
			case *ssa.Alloc:
				a.recordIns(goID, theIns)
				if a.inLoop {
					a.allocLoop[examIns.Parent()] = append(a.allocLoop[examIns.Parent()], examIns.Comment)
				}
			case *ssa.Go: // for spawning of goroutines
				if closure, ok := examIns.Call.Value.(*ssa.MakeClosure); ok && len(closure.Bindings) > 0 && a.inLoop { // if spawned in a loop
					for i, binding := range closure.Bindings {
						parentFn := examIns.Parent()
						if fvar, ok1 := binding.(*ssa.Alloc); ok1 && sliceContainsStr(a.allocLoop[parentFn], fvar.Comment) {
							a.bindingFV[examIns] = append(a.bindingFV[examIns], closure.Fn.(*ssa.Function).FreeVars[i]) // store freeVars declared within loop
						}
					}
				}
				loopID := 0
				twin := make([]int, 2)
				if a.inLoop {
					loopID++
					newGoID1 := a.insGo(examIns, goID, theIns, loopID) // loopID == 1 if goroutine in loop
					if newGoID1 != -1 {
						twin[0] = newGoID1
					}
					loopID++
				}
				newGoID2 := a.insGo(examIns, goID, theIns, loopID) // loopID == 2 if goroutine in loop, loopID == 0 otherwise
				if loopID != 0 && newGoID2 != -1 {                 //bz: record the twin goroutines
					exist := a.twinGoID[examIns]
					if exist == nil { //fill in the blank
						twin[1] = newGoID2
						a.twinGoID[examIns] = twin
					} //else: already exist, skip
				}
			case *ssa.Return:
				a.recordIns(goID, theIns)
				if examIns.Block().Comment == "if.then" || examIns.Block().Comment == "if.else" || examIns.Block().Comment == "if.done" {
					a.ifFnReturn[fn] = examIns // will be revised iteratively to eventually contain final return instruction
				}
			case *ssa.MapUpdate:
				a.insMapUpdate(examIns, goID, theIns)
			case *ssa.Select:
				readyChans = a.insSelect(examIns, goID, theIns)
				selCount = 0
				selIns = examIns
				a.selectBloc[aBlock.Index] = examIns
			case *ssa.If:
				a.recordIns(goID, theIns)
				ifIns = examIns
			default:
				a.recordIns(goID, theIns)
			}
			if ii == len(aBlock.Instrs)-1 && len(toUnlock) > 0 {
				for _, l := range toUnlock {
					if a.lockSetContainsAt(a.lockSet, l, goID) != -1 {
						a.lockSet[goID][a.lockSetContainsAt(a.lockSet, l, goID)].locFreeze = false
					}
				}
				toUnlock = []ssa.Value{}
			} else if ii == len(aBlock.Instrs)-1 && len(toRUnlock) > 0 {
				for _, l := range toRUnlock {
					if a.lockSetContainsAt(a.RlockSet, l, goID) != -1 {
						a.RlockSet[goID][a.lockSetContainsAt(a.RlockSet, l, goID)].locFreeze = false
					}
				}
				toRUnlock = []ssa.Value{}
			}
			if activeCase && readyChans[selCount] != "defaultCase" && readyChans[selCount] != "timeOut" {
				if ii == 0 {
					a.selectCaseBegin[theIns] = readyChans[selCount] // map first instruction in case to channel name
					if a.RWIns[goID][len(a.RWIns[goID])-1].ins != theIns {
						a.recordIns(goID, theIns)
					}
					a.selectCaseBody[theIns] = selIns
				} else if ii == len(aBlock.Instrs)-1 {
					a.selectCaseEnd[theIns] = readyChans[selCount] // map last instruction in case to channel name
					if a.RWIns[goID][len(a.RWIns[goID])-1].ins != theIns {
						a.recordIns(goID, theIns)
					}
					a.selectCaseBody[theIns] = selIns
				} else {
					a.selectCaseBody[theIns] = selIns
				}
			}
			if defCase {
				a.selectCaseBody[theIns] = selIns
			}
			if selDone && ii == 0 {
				if sliceContainsInsInfoAt(a.RWIns[goID], theIns) == -1 {
					a.recordIns(goID, theIns)
				}
			}
		}
		if (aBlock.Comment == "for.body" || aBlock.Comment == "rangeindex.body") && a.inLoop {
			a.inLoop = false
		}
		if activeCase && readyChans[selCount] != "defaultCase" && readyChans[selCount] != "timeOut" {
			selCount++
		} // increment case count
		if bInd == len(bVisit)-1 && len(ifEnds) > 0 {
			for _, e := range ifEnds {
				a.ifSuccEnd[e] = a.ifFnReturn[fn]
			}
		}
	}
	if len(toDefer) > 0 {
		for _, dIns := range toDefer { // ----> !!! SEE HERE: bz: the same as above, from line 307 to 347 can be separated out
			deferIns := dIns.(*ssa.Defer)
			if _, ok := deferIns.Call.Value.(*ssa.Builtin); ok {
				continue
			}
			if deferIns.Call.StaticCallee() == nil {
				continue
			} else if a.fromPkgsOfInterest(deferIns.Call.StaticCallee()) && deferIns.Call.StaticCallee().Pkg.Pkg.Name() != "sync" {
				fnName := deferIns.Call.Value.Name()
				fnName = checkTokenNameDefer(fnName, deferIns)
				a.traverseFn(deferIns.Call.StaticCallee(), fnName, goID, dIns)
			} else if deferIns.Call.StaticCallee().Name() == "Unlock" {
				lockLoc := deferIns.Call.Args[0]
				if !useNewPTA {
					a.mu.Lock()
					a.ptaCfg0.AddQuery(lockLoc)
					a.mu.Unlock()
				}
				toUnlock = append(toUnlock, lockLoc)
				lockOp := a.lockSetContainsAt(a.lockSet, lockLoc, goID) // index of locking operation
				if lockOp != -1 {
					//if a.lockSet[goID][lockOp].parentFn == theIns.Parent() && a.lockSet[goID][lockOp].locBlocInd == theIns.Block().Index { // common block
					log.Trace("Unlocking   ", lockLoc.String(), "  (", a.lockSet[goID][lockOp].locAddr.Pos(), ") removing index ", lockOp, " from: ", lockSetVal(a.lockSet, goID))
					a.lockSet[goID] = append(a.lockSet[goID][:lockOp], a.lockSet[goID][lockOp+1:]...) // remove from lockset
				}
			} else if deferIns.Call.StaticCallee().Name() == "RUnlock" {
				RlockLoc := deferIns.Call.Args[0]
				if !useNewPTA {
					a.mu.Lock()
					a.ptaCfg0.AddQuery(RlockLoc)
					a.mu.Unlock()
				}
				toRUnlock = append(toRUnlock, RlockLoc)
			} else if deferIns.Call.Value.Name() == "Done" {
				a.recordIns(goID, dIns)
				//a.RWIns[goID] = append(a.RWIns[goID], dIns)
				if !useNewPTA {
					a.mu.Lock()
					a.ptaCfg0.AddQuery(deferIns.Call.Args[0])
					a.mu.Unlock()
				}
			}
			toDefer = []ssa.Instruction{}
		}
	}
	if len(toUnlock) > 0 { // ----> !!! SEE HERE: bz: the same as above, from line 488 to 501 can be separated out
		for _, loc := range toUnlock {
			if z := a.lockSetContainsAt(a.lockSet, loc, goID); z >= 0 {
				log.Trace("Unlocking ", loc.String(), "  (", a.lockSet[goID][z].locAddr.Pos(), ") removing index ", z, " from: ", lockSetVal(a.lockSet, goID))
				a.lockSet[goID] = append(a.lockSet[goID][:z], a.lockSet[goID][z+1:]...)
			} else {
				z = a.lockSetContainsAt(a.lockSet, loc, a.goCaller[goID])
				if z == -1 {
					continue
				}
				log.Trace("Unlocking ", loc.String(), "  (", a.lockSet[goID][z].locAddr.Pos(), ") removing index ", z, " from: ", lockSetVal(a.lockSet, goID))
				a.lockSet[a.goCaller[goID]] = append(a.lockSet[a.goCaller[goID]][:z], a.lockSet[a.goCaller[goID]][z+1:]...)
			}
		}
	}
	if len(toRUnlock) > 0 { // ----> !!! SEE HERE: bz: the same as above, from line 502 to 509 can be separated out
		for _, rloc := range toRUnlock {
			if z := a.lockSetContainsAt(a.RlockSet, rloc, goID); z >= 0 {
				log.Trace("RUnlocking ", rloc.String(), "  (", a.RlockSet[goID][z].locAddr.Pos(), ") removing index ", z, " from: ", lockSetVal(a.RlockSet, goID))
				a.RlockSet[goID] = append(a.RlockSet[goID][:z], a.RlockSet[goID][z+1:]...)
			} //TODO : modify for unlock in diff thread
		}
	}
	if len(a.lockSet[goID]) > lockSetSize { // lock(s) acquired in this function that have not been released ...
		for i := lockSetSize; i < len(a.lockSet[goID]); i++ { // iterate UNreleased locks
			log.Trace("Unlocking ", a.lockSet[goID][i].locAddr.String(), "  (", a.lockSet[goID][i].locAddr.Pos(), ") removing index ", i, " from: ", lockSetVal(a.lockSet, goID))
			a.lockSet[goID] = append(a.lockSet[goID][:i], a.lockSet[goID][i+1:]...) // remove from lockset
		}
	}
	// done with all instructions in function body, now pop the function
	fnName := fn.Name()
	if fnName == a.storeFns[len(a.storeFns)-1].fnIns.Name() {
		a.updateRecords(fnName, goID, "POP  ", fn, nil)
	}
	a.visitGo()
}

//bz: let's visit all goroutines except main
func (a *analysis) visitGo() {
	for len(a.storeFns) == 0 && len(a.workList) != 0 { // finished reporting current goroutine and workList isn't empty
		nextGoInfo := a.workList[0] // get the goroutine info at head of workList
		a.workList = a.workList[1:] // pop goroutine info from head of workList
		a.newGoroutine(nextGoInfo)
	}
}

func (a *analysis) goNames(goIns *ssa.Go) string {
	var goName string
	switch anonFn := goIns.Call.Value.(type) {
	case *ssa.MakeClosure: // go call for anonymous function
		goName = anonFn.Fn.Name()
	case *ssa.Function:
		goName = anonFn.Name()
	case *ssa.TypeAssert:
		switch anonFn.X.(type) {
		case *ssa.Parameter:
			a.pointerAnalysis(anonFn.X, 0, nil)
			if a.paramFunc != nil {
				goName = a.paramFunc.Name()
			}
		}
	}
	return goName
}

// newGoroutine goes through the goroutine, logs its info, and goes through the instructions within
func (a *analysis) newGoroutine(info goroutineInfo) {
	if a.goCalls[a.goCaller[info.goID]] != nil && info.goIns == a.goCalls[a.goCaller[info.goID]].goIns {
		return // recursive spawning of same goroutine
	}
	//// bz: this will be pushed again in traverseFn later -> but we need this as record
	//newFn := &fnCallInfo{fnIns: info.entryMethod, ssaIns: info.ssaIns}
	//a.storeFns = append(a.storeFns, newFn)
	if info.goID >= len(a.RWIns) { // initialize interior slice for new goroutine
		a.RWIns = append(a.RWIns, []*insInfo{})
	}
	a.recordIns(info.goID, info.ssaIns)
	newGoInfo := &goCallInfo{goIns: info.goIns, ssaIns: info.ssaIns}
	a.goCalls[info.goID] = newGoInfo
	if DEBUG {
		if a.loopIDs[info.goID] > 0 {
			a.goInLoop[info.goID] = true
			log.Debug(strings.Repeat("-", 35), "Goroutine ", info.entryMethod.Name(), " (in loop)", strings.Repeat("-", 35), "[", info.goID, "]")
		} else {
			log.Debug(strings.Repeat("-", 35), "Goroutine ", info.entryMethod.Name(), strings.Repeat("-", 35), "[", info.goID, "]")
		}
	}
	if len(a.lockSet[a.goCaller[info.goID]]) > 0 { // carry over lockset from parent goroutine
		a.lockSet[info.goID] = a.lockSet[a.goCaller[info.goID]]
	}
	//if DEBUG {
	//	log.Debug(strings.Repeat(" ", a.levels[info.goID]), "PUSH ", info.entryMethod.Name(), " at lvl ", a.levels[info.goID])
	//}
	//a.levels[info.goID]++

	var target *ssa.Function
	switch info.goIns.Call.Value.(type) {
	case *ssa.MakeClosure:
		target = info.goIns.Call.StaticCallee()
	case *ssa.TypeAssert:
		target = a.paramFunc
	default:
		target = info.goIns.Call.StaticCallee()
	}
	if target != nil {
		//a.visitAllInstructions(target, info.goID)
		a.traverseFn(target, target.Name(), info.goID, info.ssaIns)
	}
}

// recordIns places newly encountered instructions into data structure for analyzing later
func (a *analysis) recordIns(goID int, newIns ssa.Instruction) {
	newInsInfo := &insInfo{ins: newIns}
	stack := make([]*fnCallInfo, len(a.storeFns))
	copy(stack, a.storeFns)
	newInsInfo.stack = stack
	a.RWIns[goID] = append(a.RWIns[goID], newInsInfo)
}

// exploredFunction determines if we already visited this function
func (a *analysis) exploredFunction(fn *ssa.Function, goID int, theIns ssa.Instruction) bool {
	var csSlice []*ssa.Function
	var csStr string

	if theIns == nil { //bz: this happens when analyzing entry point, or go routine -> i only need a record
		csStr = ""
	} else {
		if a.fromExcludedFns(fn) {
			return true
		}
		if a.efficiency && !a.fromPkgsOfInterest(fn) { // for temporary debugging purposes only
			return true
		}
		if sliceContainsInsInfoAt(a.RWIns[goID], theIns) >= 0 {
			return true
		}
		theFn := fnCallInfo{fn, theIns}
		if a.efficiency && sliceContainsFnCall(a.storeFns, theFn) { // for temporary debugging purposes only
			return true
		}
		var visitedIns []*insInfo
		if len(a.RWIns) > 0 {
			visitedIns = a.RWIns[goID]
		}
		csSlice, csStr = insToCallStack(visitedIns)
		if sliceContainsFnCtr(csSlice, fn) > trieLimit {
			return true
		}
	}

	fnKey := fnInfo{
		fnName:     fn,
		contextStr: csStr,
	}
	if existingTrieNode, ok := a.trieMap[fnKey]; ok {
		existingTrieNode.budget++ // increment the number of times for calling the function under the current context by one
	} else {
		newTrieNode := trie{
			fnName:    fn.Name(),
			budget:    1,
			fnContext: csSlice,
		}
		a.trieMap[fnKey] = &newTrieNode
	}

	if theIns == nil { //bz: for go call, skip the checking of budget
		return false
	}
	return a.trieMap[fnKey].isBudgetExceeded()
}

// updateRecords will print out the stack trace
func (a *analysis) updateRecords(fnName string, goID int, pushPop string, theFn *ssa.Function, theIns ssa.Instruction) {
	if pushPop == "POP  " {
		a.storeFns = a.storeFns[:len(a.storeFns)-1]
		a.levels[goID]--
	}
	if DEBUG {
		log.Debug(strings.Repeat(" ", a.levels[goID]), pushPop, theFn.String(), " at lvl ", a.levels[goID])
	}
	if pushPop == "PUSH " {
		newFn := &fnCallInfo{ssaIns: theIns, fnIns: theFn}
		a.storeFns = append(a.storeFns, newFn)
		a.levels[goID]++
	}
}

//bz: call this before any call to visitAllInstructions
func (a *analysis) traverseFn(fn *ssa.Function, fnName string, goID int, theIns ssa.Instruction) {
	if !a.exploredFunction(fn, goID, theIns) {
		a.updateRecords(fnName, goID, "PUSH ", fn, theIns)
		if theIns != nil { //bz: this happens when analyzing entry point
			a.recordIns(goID, theIns)
		}
		a.visitAllInstructions(fn, goID)
	}
	if isWriteIns(theIns) || a.isReadIns(theIns) { //bz: this happens when analyzing entry point, or from newGoroutine -> will always be false
		a.updateLockMap(goID, theIns)
		a.updateRLockMap(goID, theIns)
	}
}

// getRcvChan returns channel name of receive Op
func (a *analysis) getRcvChan(ins *ssa.UnOp) string {
	for ch, rIns := range a.chanRcvs {
		if sliceContainsRcv(rIns, ins) { // channel receive
			return ch
		}
	}
	return ""
}

func (a *analysis) getSndChan(ins *ssa.Send) string {
	for ch, sIns := range a.chanSnds {
		if sliceContainsSnd(sIns, ins) { // channel receive
			return ch
		}
	}
	return ""
}

// isReadySel returns whether or not the channel is awaited on (and ready) by a select statement
func (a *analysis) isReadySel(ch string) bool {
	for _, chStr := range a.selReady {
		if sliceContainsStr(chStr, ch) {
			return true
		}
	}
	return false
}

//stackGo prints the callstack of a goroutine -> bz: not used now
func (a *analysis) stackGo() {
	//if a.getGo { // print call stack of each goroutine
	for i := 0; i < len(a.RWIns); i++ {
		name := "main"
		if i > 0 {
			name = a.goNames(a.goCalls[i].goIns)
		}
		if a.goInLoop[i] {
			log.Debug("Goroutine ", i, "  --  ", name, strings.Repeat(" *", 10), " spawned by a loop", strings.Repeat(" *", 10))
		} else {
			log.Debug("Goroutine ", i, "  --  ", name)
		}
		if i > 0 {
			log.Debug("call stack: ")
		}
		var pathGo []int
		goID := i
		for goID > 0 {
			pathGo = append([]int{goID}, pathGo...)
			temp := a.goCaller[goID]
			goID = temp
		}
		if !allEntries {
			for q, eachGo := range pathGo {
				eachStack := a.goStack[eachGo]
				for k, eachFn := range eachStack {
					if k == 0 {
						log.Debug("\t ", strings.Repeat(" ", q), "--> Goroutine: ", eachFn.fnIns.Name(), "[", a.goCaller[eachGo], "] ", a.prog.Fset.Position(eachFn.ssaIns.Pos()))
					} else {
						log.Debug("\t   ", strings.Repeat(" ", q), strings.Repeat(" ", k), eachFn.fnIns.Name(), " ", a.prog.Fset.Position(eachFn.ssaIns.Pos()))
					}
				}
			}
		}
	}
	//}
}
