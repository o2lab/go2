package main

import (
	"fmt"
	"github.com/april1989/origin-go-tools/go/packages"
	"github.com/april1989/origin-go-tools/go/ssa"
	"github.com/april1989/origin-go-tools/go/ssa/ssautil"
	"github.com/april1989/origin-go-tools/go/util"
	"github.com/briandowns/spinner"
	log "github.com/sirupsen/logrus"
	"go/token"
	"go/types"
	"strconv"
	"strings"
	"time"
	"unicode"
)

//bz: global -> all use the same one
var spin *spinner.Spinner

func doStartLog(_log string) {
	if goTest {
		return
	}
	if turnOnSpinning {
		if spin == nil {
			spin = spinner.New(spinner.CharSets[9], 100*time.Millisecond) // Build our new spinner
			spin.Suffix = _log
			spin.Start()
		} else {
			spin.Suffix = _log
			spin.Restart()
		}
	} else {
		log.Info(_log)
	}
}

func doEndLog(args ...interface{}) {
	if goTest {
		return
	}
	if turnOnSpinning {
		spin.FinalMSG = fmt.Sprint(args[0]) + "\n"
		spin.Stop()
	} else {
		log.Info(args...)
	}
}

//TODO: bz: what is the diff between isLocalAddr
func isLocal(ins ssa.Instruction) bool {
	switch rw := ins.(type) {
	case *ssa.UnOp:
		if faddr, ok := rw.X.(*ssa.FieldAddr); ok {
			if _, local := faddr.X.(*ssa.Alloc); local {
				return true
			}
		}
	case *ssa.Store:
		if faddr, ok := rw.Addr.(*ssa.FieldAddr); ok {
			if _, local := faddr.X.(*ssa.Alloc); local {
				return true
			}
		}
	}
	return false
}

// isLocalAddr returns whether location is a local address or not
func isLocalAddr(location ssa.Value) bool {
	if location.Pos() == token.NoPos {
		return true
	}
	switch loc := location.(type) {
	case *ssa.Parameter:
		_, ok := loc.Type().(*types.Pointer)
		return !ok
	case *ssa.FieldAddr:
		isLocalAddr(loc.X)
	case *ssa.IndexAddr:
		isLocalAddr(loc.X)
	case *ssa.UnOp:
		isLocalAddr(loc.X)
	case *ssa.Alloc:
		if !loc.Heap {
			return true
		}
	default:
		return false
	}
	return false
}

// isSynthetic returns whether fn is synthetic or not
func isSynthetic(fn *ssa.Function) bool { // ignore functions that are NOT true source functions
	return fn.Synthetic != "" || fn.Pkg == nil
}

//bz: whether the function name follows the go test form: https://golang.org/pkg/testing/
// copied from /gopta/go/pointer/gen.go -> remember to update
func isGoTestForm(name string) bool {
	if strings.Contains(name, "$") {
		return false //closure
	}
	if strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Benchmark") || strings.HasPrefix(name, "Example") {
		return true
	}
	return false
}

func pkgSelection(initial []*packages.Package, multiSamePkgs bool) ([]*ssa.Package, *ssa.Program, []*ssa.Package) {
	var prog *ssa.Program
	var pkgs []*ssa.Package
	var mainPkgs []*ssa.Package //bz: selected

	doStartLog("Building SSA code for entire program...")
	prog, pkgs = ssautil.AllPackages(initial, 0)
	if len(pkgs) == 0 {
		log.Errorf("SSA code could not be constructed due to type errors. ")
	}
	prog.Build()
	noFunc := len(ssautil.AllFunctions(prog))
	mainPkgs = ssautil.MainPackages(pkgs)
	if len(mainPkgs) == 0 {
		log.Errorf("No main function detected. ")
		return nil, prog, pkgs
	}
	doEndLog("Done  -- SSA code built. " + strconv.Itoa(len(pkgs)) + " packages and " + strconv.Itoa(noFunc) + " functions detected. ")

	if len(mainPkgs) == 1 {
		return mainPkgs, prog, pkgs
	}

	if allEntries { // no selection from user required
		fmt.Println(len(mainPkgs), "main() entry-points identified. ")
		log.Info("Iterating through all entry point options...")
		return mainPkgs, prog, pkgs
	}

	// Provide entry-point options and retrieve user selection
	fmt.Println(len(mainPkgs), "main() entry-points identified: ")
	if multiSamePkgs { //bz: all duplicate pkg name, identify by source file loc
		for i, ep := range mainPkgs {
			if ep.Pkg.Path() != "command-line-arguments" {
				//bz: normal pkg
				fmt.Println("Option", i+1, ": ", ep.String())
				continue
			}
			//bz: duplicate mains
			loc := ep.Pkg.Scope().Child(0).String() //bz: too much info, cannot modify lib func
			idx := strings.Index(loc, ".go")        //this is the source file loc
			loc = loc[:idx+3]
			fmt.Println("Option", i+1, ": ", ep.String(), "("+loc+")")
		}
	} else { //bz: normal case
		//bz: we need a check to mark duplicate pkg in a program, also tell user the diff
		pkgMap := make(map[string][]*ssa.Package)
		if !multiSamePkgs { //bz: must be duplicate pkg ... skip
			for _, pkg := range mainPkgs {
				key := pkg.String()
				exist := pkgMap[key]
				if exist == nil {
					exist = make([]*ssa.Package, 1)
					exist[0] = pkg
				} else {
					exist = append(exist, pkg)
				}
				pkgMap[key] = exist
			}
		}

		for i, ep := range mainPkgs {
			//bz: check if exist duplicates
			pkgStr := ep.String()
			if dup := pkgMap[pkgStr]; len(dup) > 1 {
				isTest := false
				for memName, _ := range ep.Members {
					if isGoTestForm(memName) {
						isTest = true
						break
					}
				}
				if isTest {
					//bz: this is test cases for main pkg, but go ssa/pkg builder cannot identify the diff, so they use the same pkg name but different contents
					ep.IsMainTest = true //bz: mark it -> change the code in pta is too complex, just mark it
					fmt.Println("Option", i+1, ": ", ep.String()+".main.test")
					continue
				}
			}
			//all other
			fmt.Println("Option", i+1, ": ", ep.String())
		}
	}
	fmt.Print("Enter option number of choice: \n")
	fmt.Print("*** use space delimiter for multiple selections *** \n")
	fmt.Print("*** use \"-\" for a range of selections *** \n")
	fmt.Print("*** if selecting a test pkg (i.e., ending in .test), please select ONE at a time *** \n")
	mains, err := getUserSelectionPkg(mainPkgs, pkgs)
	for mains == nil {
		fmt.Println(err)
		fmt.Println("Enter option number of choice: ")
		mains, err = getUserSelectionPkg(mainPkgs, pkgs)
	}

	return mains, prog, pkgs
}

//bz: handle wrong input
func getUserSelectionPkg(mainPkgs []*ssa.Package, pkgs []*ssa.Package) ([]*ssa.Package, string) {
	var mainInd string
	var enterAt string
	var mains []*ssa.Package
	userEP := false // user specified entry function
	max := len(mainPkgs) //max length
	fmt.Scan(&mainInd)
	if mainInd == "-" { //bz: entry point is a random function, not test or main
		fmt.Print("Enter function name to begin analysis from: ")
		fmt.Scan(&enterAt)
		for _, p := range pkgs {
			if p != nil {
				if fnMem, okf := p.Members[enterAt]; okf { // package contains function to enter at
					userEP = true
					mains = append(mainPkgs, p)
					//entryFn = enterAt // start analysis at user specified function TODO: bz: what is the use of entryFn here?
					_ = fnMem
				}
			}
		}
		if !userEP { // TODO: request input again
			return nil, "Function not found. "
		}
	} else if strings.Contains(mainInd, ",") { // multiple selections
		selection := strings.Split(mainInd, ",")
		for _, s := range selection {
			i, _ := strconv.Atoi(s) // convert to integer
			if i - 1 >= max {
				return nil, "Selection out of scope: " + s
			}
			mains = append(mains, mainPkgs[i-1])
		}
	} else if strings.Contains(mainInd, "-") { // selected range
		selection := strings.Split(mainInd, "-")
		begin, _ := strconv.Atoi(selection[0])
		end, _ := strconv.Atoi(selection[1])
		if begin - 1 >= max || end - 1 >= max {
			return nil, "Selection out of scope: " + mainInd
		}
		for i := begin; i <= end; i++ {
			mains = append(mains, mainPkgs[i-1])
		}
	} else if i, err0 := strconv.Atoi(mainInd); err0 == nil {
		if i - 1 >= max {
			return nil, "Selection out of scope: " + strconv.Itoa(i)
		}
		mains = append(mains, mainPkgs[i-1])
	} else {
		return nil, "Unrecognized input, try again."
	}
	return mains, ""
}

//bz:
func getUserSelectionFn(testFns []*ssa.Function) ([]*ssa.Function, string) {
	var testSelect string
	var selectedFns []*ssa.Function
	max := len(testFns)
	fmt.Scan(&testSelect)
	if strings.Contains(testSelect, ",") { // multiple selections
		selection := strings.Split(testSelect, ",")
		for _, s := range selection {
			i, _ := strconv.Atoi(s)                         // convert to integer
			if i - 1 >= max {
				return nil, "Selection out of scope: " + strconv.Itoa(i)
			}
			selectedFns = append(selectedFns, testFns[i-1]) // TODO: analyze multiple tests concurrently
		}
	} else if strings.Contains(testSelect, "-") { // selected range
		selection := strings.Split(testSelect, "-")
		begin, _ := strconv.Atoi(selection[0])
		end, _ := strconv.Atoi(selection[1])
		if begin - 1 >= max || end - 1 >= max {
			return nil, "Selection out of scope: " + testSelect
		}
		for i := begin; i <= end; i++ {
			selectedFns = append(selectedFns, testFns[i-1]) // TODO: analyze multiple tests concurrently
		}
	} else if i, err0 := strconv.Atoi(testSelect); err0 == nil { // single selection
		if i - 1 >= max {
			return nil, "Selection out of scope: " + strconv.Itoa(i)
		}
		selectedFns = append(selectedFns, testFns[i-1]) // TODO: analyze multiple tests concurrently
	} else if unicode.IsLetter(rune(testSelect[0])) { // user input name of test function TODO:bz: only 1 allowed?
		for _, fn := range testFns {
			if fn.Name() == testSelect {
				selectedFns = append(selectedFns, fn)
			}
		}
		if len(selectedFns) == 0 {
			return nil, "Function not found."
		}
	} else {
		return nil, "Unrecognized input, try again."
	}
	return selectedFns, ""
}


//bz: default behavior is like this:
// use the user input dir/getwd as default scope; if scope in yml != nil, already add it to pta scope (when decode yml)
//Update: if is multiSamePkgs, then main must be "command-line-arguments"
func determineScope(main *ssa.Package, pkgs []*ssa.Package) []string {
	//bz: there are these cases:
	// 1. user run under root dir of a project -> find the go.mod
	// 2. user input is a .go file -> command-line-arguments
	// 3. user run under a dir, but only one .go file (which must be the file with main function) -> command-line-arguments
	// 4. user run under a dir, but has subdirs and/or .go files; some dir has go.mod, some do not
    //NOTE: multiSamePkgs may happen in a sub dir or the usr input dir

	var scope []string
	tmp := ""
	if main.Pkg != nil {
		tmp = main.Pkg.Path() //tmp cannot be nil if is "command-line-arguments"
	}
    if tmp == "command-line-arguments" {
		scope = append(scope, "command-line-arguments")
	} else if len(pkgs) == 1 {
		//one/multi files or sub dirs with pkg name -> main == pkgs[0]
		modScope := util.GetScopeFromGOModRecursive(tmp, userDir)
		if modScope != "" {
			scope = append(scope, modScope)
		} else { //cannot locate the correct go.mod
			scope = append(scope, tmp)
		}
	} else { //multiple pkg or multiple go.mod -> see if we can find go.mod
		modScope := util.GetScopeFromGOMod(userDir)
		if modScope != "" {
			scope = append(scope, modScope)
		} else { // multiple pkgs: 1st pkg might be the root dir that user run gorace,
			// need to check with other pkgs, since they all share the most left pkg path
			// UPDATE: pkgs[i] can be nil in linux
			if pkgs[0] != nil && pkgs[0].Pkg != nil {
				tmp = pkgs[0].Pkg.Path()
			}
			modScope := util.GetScopeFromGOModRecursive(tmp, userDir)
			if modScope != "" {
				scope = append(scope, modScope)
			} else if len(main.Files()) == 1 { //even though it has a pkg name, but only one .go file inside
				scope = append(scope, "command-line-arguments")
			} else {
				//multiple .go files or subdirs, and cannot locate the correct go.mod
				for _, pkg := range pkgs {
					if pkg == nil || pkg.Pkg == nil {
						continue
					}
					p1 := pkg.Pkg.Path()
					if strings.HasPrefix(p1, tmp) {
						continue
					} else { //p1 has shorter path, use this
						tmp = p1
					}
				}
				//until find the shortest path among all pkgs
				scope = append(scope, tmp)
			}
		}
	}

	if len(inputScope) > 0 { //user input
		for _, s := range inputScope {
			scope = append(scope, s)
		}
	}

	if DEBUG { //bz: debug use
		for _, s := range scope {
			fmt.Println(" - ", s)
		}
	}

	return scope
}


//var scope = make([]string, 1)
////if pkgs[0] != nil { // Note: only if main dir contains main.go.
////	scope[0] = pkgs[0].Pkg.Path() //bz: the 1st pkg has the scope info == the root pkg or default .go input
////} else
//if pkgs[0] == nil && len(pkgs) == 1 {
//	log.Fatal("Error: No packages detected. Please verify directory provided contains Go Files. ")
//} else if len(pkgs) > 1 && pkgs[1] != nil && !strings.Contains(pkgs[1].Pkg.Path(), "/") {
//	scope[0] = pkgs[1].Pkg.Path()
//} else if len(pkgs) > 1 {
//	scope[0] = strings.Split(pkgs[1].Pkg.Path(), "/")[0] + "/" + strings.Split(pkgs[1].Pkg.Path(), "/")[1]
//	if strings.Contains(pkgs[1].Pkg.Path(), "gorace") {
//		scope[0] += "/gorace"
//	}
//	if strings.Contains(pkgs[1].Pkg.Path(), "ethereum") {
//		scope[0] += "/go-ethereum"
//	}
//	if strings.Contains(pkgs[1].Pkg.Path(), "grpc") {
//		scope[0] += "/go-grpc"
//	}
//} else if len(pkgs) == 1 {
//	scope[0] = pkgs[0].Pkg.Path()
//}
////scope[0] = "google.golang.org/grpc"

func getSrcPos(address ssa.Value) token.Pos {
	var position token.Pos
	switch param := address.(type) {
	case *ssa.Parameter:
		position = param.Pos()
	case *ssa.FieldAddr:
		position = param.Pos()
	case *ssa.Alloc:
		position = param.Pos()
	case *ssa.FreeVar:
		position = param.Pos()
	}
	return position
}

func checkTokenName(varName string, theIns ssa.Instruction) string {
	if strings.HasPrefix(varName, "t") { // function name begins with letter t
		if _, err := strconv.Atoi(string([]rune(varName)[1:])); err == nil { // function name after first character look like an integer
			switch examIns := theIns.(type) {
			case *ssa.Call:
				switch callVal := examIns.Call.Value.(type) {
				case *ssa.Function:
					return callVal.Name()
				case *ssa.MakeClosure:
					return callVal.Fn.Name()
				default:
					return callVal.Type().String()
				}
			case *ssa.UnOp:
				switch unOpX := examIns.X.(type) {
				case *ssa.FieldAddr:
					switch faX := unOpX.X.(type) {
					case *ssa.Parameter:
						return faX.Name()
					case *ssa.Alloc:
						return faX.Name()
					default:
						// debug
					}
				default:
					// debug
				}
			case *ssa.Store:
				switch addr := examIns.Addr.(type) {
				case *ssa.FieldAddr:
					switch faX := addr.X.(type) {
					case *ssa.Parameter:
						return faX.Name()
					}
				}
			default:
				// debug
			}
		}
	}
	return varName
}

//// checkTokenName will return original name of an input function rather than a token
//func checkTokenName(fnName string, theIns *ssa.Call) string {
//	if strings.HasPrefix(fnName, "t") { // function name begins with letter t
//		if _, err := strconv.Atoi(string([]rune(fnName)[1:])); err == nil { // function name after first character look like an integer
//			switch callVal := theIns.Call.Value.(type) {
//			case *ssa.MakeClosure:
//				fnName = callVal.Fn.Name()
//			default:
//				fnName = callVal.Type().String()
//			}
//		}
//	}
//	return fnName
//}

// checkTokenNameDefer will return original name of an input defered function rather than a token
func checkTokenNameDefer(fnName string, theIns *ssa.Defer) string {
	if strings.HasPrefix(fnName, "t") { // function name begins with letter t
		if _, err := strconv.Atoi(string([]rune(fnName)[1:])); err == nil { // function name after first character look like an integer
			switch callVal := theIns.Call.Value.(type) {
			case *ssa.MakeClosure:
				fnName = callVal.Fn.Name()
			default:
				fnName = callVal.Type().String()
			}
		}
	}
	return fnName
}
