package main

import (
	"bufio"
	"fmt"
	"github.com/april1989/origin-go-tools/go/packages"
	"github.com/april1989/origin-go-tools/go/ssa"
	"github.com/april1989/origin-go-tools/go/ssa/ssautil"
	"github.com/briandowns/spinner"
	log "github.com/sirupsen/logrus"
	"go/token"
	"go/types"
	"os"
	"strconv"
	"strings"
	"time"
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
	}
	doEndLog("Done  -- SSA code built. " + strconv.Itoa(len(pkgs)) + " packages and " + strconv.Itoa(noFunc) + " functions detected. ")

	var mainInd string
	var enterAt string
	var mains []*ssa.Package
	userEP := false // user specified entry function
	if len(mainPkgs) > 1 {
		//bz: we need a check to mark duplicate pkg in a program, also tell user the diff
		pkgMap := make(map[string][]*ssa.Package)
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
		//// Provide entry-point options and retrieve user selection
		//fmt.Println(len(mainPkgs), "main() entry-points identified: ")
		//for i, ep := range mainPkgs {
		//	//bz: check if exist duplicate
		//	pkgStr := ep.String()
		//	if dup := pkgMap[pkgStr]; len(dup) > 1 {
		//		isTest := false
		//		for memName, _ := range ep.Members {
		//			if isGoTestForm(memName) {
		//				isTest = true
		//				break
		//			}
		//		}
		//		if isTest {
		//			//bz: this is test cases for main pkg, but go ssa/pkg builder cannot identify the diff, so they use the same pkg name but different contents
		//			ep.IsMainTest = true //bz: mark it -> change the code in pta is too complex, just mark it
		//			fmt.Println("Option", i+1, ": ", ep.String()+".main.test")
		//			continue
		//		}
		//	}
		//	//all other
		//	fmt.Println("Option", i+1, ": ", ep.String())
		//}
		if allEntries { // no selection from user required
			fmt.Println(len(mainPkgs), "main() entry-points identified. ")
			for pInd := 0; pInd < len(mainPkgs); pInd++ {
				mains = append(mains, mainPkgs[pInd])
			}
			log.Info("Iterating through all entry point options...")
		} else {
			// Provide entry-point options and retrieve user selection
			fmt.Println(len(mainPkgs), "main() entry-points identified: ")
			if multiSamePkgs { //bz: all duplicate pkg name, identify by source file loc
				for i, ep := range mainPkgs {
					loc := ep.Pkg.Scope().Child(0).String() //bz: too much info, cannot modify lib func
					idx := strings.Index(loc, ".go") //this is the source file loc
					loc = loc[:idx + 3]
					fmt.Println("Option", i+1, ": ", ep.String(), "(" + loc + ")")
				}
			}else {
				for i, ep := range mainPkgs {
					//bz: check if exist duplicate
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
			fmt.Print("*** if selecting a test folder (ie. ending in .test), please select ONE at a time *** \n")
			fmt.Scan(&mainInd)
			if mainInd == "-" {
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
				if !userEP {
					fmt.Print("Function not found. ") // TODO: request input again
				}
			} else if strings.Contains(mainInd, ",") { // multiple selections
				selection := strings.Split(mainInd, ",")
				for _, s := range selection {
					i, _ := strconv.Atoi(s) // convert to integer
					mains = append(mains, mainPkgs[i-1])
				}
			} else if strings.Contains(mainInd, "-") { // selected range
				selection := strings.Split(mainInd, "-")
				begin, _ := strconv.Atoi(selection[0])
				end, _ := strconv.Atoi(selection[1])
				for i := begin; i <= end; i++ {
					mains = append(mains, mainPkgs[i-1])
				}
			} else if i, err0 := strconv.Atoi(mainInd); err0 == nil {
				mains = append(mains, mainPkgs[i-1])
			}
		}
	} else {
		mains = mainPkgs
	}
	return mains, prog, pkgs
}

//bz: default behavior is like this:
// use the user input dir/getwd as default scope; if scope in yml != nil, already add it to pta scope (when decode yml)
func determineScope(main *ssa.Package, pkgs []*ssa.Package, multiSamePkgs bool) {
	if PTAscope != nil && len(PTAscope) > 0 {
		//bz: already determined, return
		return
	}
	//bz: there are three cases:
	// 1. user run under root dir of a project -> find the go.mod
	// 2. user input is a .go file -> command-line-arguments
	// 3. user run under a dir, but only one .go file (which must be the file with main function) -> command-line-arguments
	// 4. user run under a dir, but has subdirs and/or .go files -> ??

	if multiSamePkgs {
		PTAscope = append(PTAscope, "command-line-arguments")
	}else if len(pkgs) == 1 {
		pkg := pkgs[0]
		files := pkg.Files()
		tmp := pkg.Pkg.Path()
		if len(files) == 1 && tmp == "command-line-arguments" { //only 1 file -> no specific pkg
			PTAscope = append(PTAscope, "command-line-arguments")
		} else { //one/multi files or sub dirs with pkg name
			modScope := recursiveGetScopeFromGoMod(tmp)
			if modScope != "" {
				PTAscope = append(PTAscope, modScope)
			} else { //cannot locate the correct go.mod
				PTAscope = append(PTAscope, tmp)
			}
		}
	} else { //multiple pkg -> see if we can find go.mod
		modScope := getScopeFromGOMod("")
		if modScope != "" {
			PTAscope = append(PTAscope, modScope)
		} else { // multiple pkgs: 1st pkg might be the root dir that user run gorace,
			// need to check with other pkgs, since they all share the most left pkg path
			tmp := pkgs[0].Pkg.Path()
			modScope := recursiveGetScopeFromGoMod(tmp)
			if modScope != "" {
				PTAscope = append(PTAscope, modScope)
			} else {
				//cannot locate the correct go.mod
				for _, pkg := range pkgs {
					p1 := pkg.Pkg.Path()
					if strings.HasPrefix(p1, tmp) {
						continue
					} else { //p1 has shorter path, use this
						tmp = p1
					}
				}
				//until find the shortest path among all pkgs
				PTAscope = append(PTAscope, tmp)
			}
		}
	}

	if len(inputScope) > 0 { //user input
		for _, s := range inputScope {
			PTAscope = append(PTAscope, s)
		}
	}

	if DEBUG { //bz: debug use
		for _, s := range PTAscope {
			fmt.Println(" - ",s)
		}
	}
}

//bz:
func recursiveGetScopeFromGoMod(tmpScope string) string {
	curPath, err := os.Getwd() //current
	if err != nil {
		panic("Error while os.Getwd: " + err.Error())
	}
	idx := strings.LastIndex(curPath, "/")
	for idx > 0 {
		checkPath := curPath[:idx]
		modScope := getScopeFromGOMod(checkPath)
		if modScope != "" && strings.HasPrefix(tmpScope, modScope){
			return modScope
		}else{
			idx = strings.LastIndex(checkPath, "/")
		}
	}
	return ""
}

//bz:
func getScopeFromGOMod(path string) string {
	if path == "" {
		var err error
		path, err = os.Getwd() //current working directory == project path
		if err != nil {
			panic("Error while os.Getwd: " + err.Error())
		}
	}

	gomodFile, err := os.Open(path + "/go.mod") // For read access.
	if err != nil {
		return "" //no such file
	}
	defer gomodFile.Close()
	scanner := bufio.NewScanner(gomodFile)
	var mod string
	for scanner.Scan() {
		s := scanner.Text()
		if strings.HasPrefix(s, "module ") {
			mod = s
			break //this is the line "module xxx.xxx.xx/xxx"
		}
	}
	if mod == "" {
		return "" //not format as expected
	}
	if err2 := scanner.Err(); err2 != nil {
		return "" //scan error
	}
	parts := strings.Split(mod, " ")
	return parts[1]
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
