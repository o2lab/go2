package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/rogpeppe/go-internal/testenv"
	"github.com/sirupsen/logrus"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"golang.org/x/sync/semaphore"
	"io/ioutil"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

var (
	haltOnError = flag.Bool("halt", false, "halt on error")
	listErrors  = flag.Bool("errlist", false, "list errors")
	testFiles   = flag.String("files", "", "space-separated list of test files")
	parallel    = flag.Int("cpu", 0, "max parallelism")
)

var tests = []string{
	"../tests/atomics.go", // race condition - no shared memory
	"../tests/cfg.go",
	"../tests/cg.go",
	"../tests/chan_receive.go",
	"../tests/context1.go",
	"../tests/defer.go",
	"../tests/defer_mut_ex.go",
	"../tests/fields.go",
	"../tests/fzf_wg.go",
	"../tests/global_ownership.go",
	"../tests/if.go",
	"../tests/if_loop.go",
	"../tests/if_mut_ex.go",
	"../tests/ifunlockProtected.go",
	"../tests/ifunlockUnprotected.go",
	"../tests/k8s_issue80269.go",
	"../tests/local.go", // TODO: also include local_loop.go
	"../tests/local_thread_safe.go",
	"../tests/lock.go",
	"../tests/lock_grpc_sync.go",
	"../tests/lock_mixed.go",
	"../tests/lock_multiple_deferred.go",
	"../tests/lock_twice.go",
	"../tests/map_race.go",
	"../tests/multi_sends.go",
	"../tests/multiple_waitgroups.go",
	"../tests/mutual_exclusion1.go",
	"../tests/mutual_exclusion2.go",
	"../tests/ownership1.go",
	"../tests/race_cfg.go",
	"../tests/race_example1.go",
	"../tests/race_example2.go",
	"../tests/race_example3.go",
	"../tests/runc_simple.go",
	"../tests/rwmutex_lock.go",
	"../tests/select.go",
	"../tests/select_all_ready.go",
	//"tests/select_multiple.go",
	"../tests/select_nonblock.go",
	"../tests/select_nonblock_fp.go",
	"../tests/select_rpc.go",
	"../tests/select_sends.go",
	"../tests/select_timeout.go",
	"../tests/send_after_select.go",
	"../tests/single_case_select.go",
	"../tests/stack.go",
	"../tests/test1.go",
	"../tests/test_neo.go",
	"../tests/waitgroup.go",
	"../tests/wrong_waitgroup.go",
	"../tests/GoBench/Cockroach/27659/main.go",
	//"tests/GoBench/Cockroach/35501/main.go", // considered special case due to Go compiler's handling of for-each loops
	"../tests/GoBench/Etcd/4876/main.go",
	"../tests/GoBench/Etcd/8149/main.go", // multiple select handling
	"../tests/GoBench/Etcd/9446/main.go",
	"../tests/GoBench/Grpc/1748/main.go",
	"../tests/GoBench/Grpc/1862/main.go",
	"../tests/GoBench/Grpc/3090/main.go",
	"../tests/GoBench/Istio/8144/main.go",
	"../tests/GoBench/Istio/8214/main.go",
	"../tests/GoBench/Istio/8967/main.go", // WIP - chan struct{}
	"../tests/GoBench/Istio/16742/main.go",
	"../tests/GoBench/Kubernetes/79631/main.go",
	"../tests/GoBench/Kubernetes/80284/main.go",
	"../tests/GoBench/Kubernetes/81091/main.go", // WIP - chan struct{}
	"../tests/GoBench/Kubernetes/81148/main.go",
	"../tests/GoBench/Kubernetes/88331/main.go",
	"../tests/GoBench/Serving/3148/main.go",
	//"tests/GoBench/Serving/4908/main.go",
	//"tests/GoBench/Serving/6171/main.go",
	"../tests/godel2/ch-as-lock-race/main.go",
	"../tests/godel2/deposit-race/main.go",
	//"tests/godel2/prod-cons-race/main.go",
	"../tests/godel2/simple-race/main.go",
	//"tests/godel2/dine3-chan-race/main.go", // multiple nested selects
	//"tests/godel2/dine5-chan-race/main.go",
}

var passed int32 = 0

var fset = token.NewFileSet()

// Positioned errors are of the form "(Read|Write) of ... at filename:line:column".
var posMsgRx = regexp.MustCompile(`^(.*) at (.*:[0-9]+:[0-9]+)$`)

// splitError splits an error's error message into a position string
// and the actual error message. If there's no position information,
// pos is the empty string, and msg is the entire error message.
//
func splitError(err error) (pos, msg string) {
	msg = err.Error()
	if m := posMsgRx.FindStringSubmatch(msg); len(m) == 3 {
		msg = m[1]
		pos = m[2]
	}
	return
}

// RACE comments must start with text `RACE "rx"` or `RACE rx` where
// rx is a regular expression that matches the expected error message.
// Space around "rx" or rx is ignored. Use the form `RACE HERE "rx"`
// for error messages that are located immediately after rather than
// at a token's position.
//
var errRx = regexp.MustCompile(`^ *RACE *(HERE)? *"?([^"]*)"?`)

// errMap collects the regular expressions of ERROR comments found
// in files and returns them as a map of error positions to error messages.
//
func errMap(t *testing.T, testname string, files []*ast.File) map[string][]string {
	// map of position strings to lists of error message patterns
	errmap := make(map[string][]string)

	for _, file := range files {
		filename := fset.Position(file.Package).Filename
		src, err := ioutil.ReadFile(filename)
		if err != nil {
			t.Fatalf("%s: could not read %s", testname, filename)
		}
		filename, _ = filepath.Abs(filename)

		var s scanner.Scanner
		s.Init(fset.AddFile(filename, -1, len(src)), src, nil, scanner.ScanComments)
		var prev token.Pos // position of last non-comment, non-semicolon token
		var here token.Pos // position immediately after the token at position prev

	scanFile:
		for {
			pos, tok, lit := s.Scan()
			switch tok {
			case token.EOF:
				break scanFile
			case token.COMMENT:
				if lit[1] == '*' {
					lit = lit[:len(lit)-2] // strip trailing */
				}
				if s := errRx.FindStringSubmatch(lit[2:]); len(s) == 3 {
					pos := prev
					if s[1] == "HERE" {
						pos = here
					}
					p := fset.Position(pos).String()
					errmap[p] = append(errmap[p], strings.TrimSpace(s[2]))
				}
			case token.SEMICOLON:
				// ignore automatically inserted semicolon
				if lit == "\n" {
					continue scanFile
				}
				fallthrough
			default:
				prev = pos
				var l int // token length
				if tok.IsLiteral() {
					l = len(lit)
				} else {
					l = len(tok.String())
				}
				here = prev + token.Pos(l)
			}
		}
	}

	return errmap
}

func eliminate(t *testing.T, errmap map[string][]string, racyStackTops []error) bool {
	for _, err := range racyStackTops {
		pos, gotMsg := splitError(err)
		list := errmap[pos]
		index := -1 // list index of matching message, if any
		// we expect one of the messages in list to match the error at pos
		for i, wantRx := range list {
			rx, err := regexp.Compile(wantRx)
			if err != nil {
				t.Errorf("%s: %v", pos, err)
				continue
			}
			if rx.MatchString(gotMsg) {
				index = i
				break
			}
		}
		if index >= 0 {
			// eliminate from list
			if n := len(list) - 1; n > 0 {
				// not the last entry - swap in last element and shorten list by 1
				list[index] = list[n]
				errmap[pos] = list[:n]
			} else {
				// last entry - remove list from map
				delete(errmap, pos)
			}
		} else {
			t.Errorf("No race expected but got %q\n  %s", gotMsg, pos)
			return false
		}
	}
	return true
}

func runChecker(t *testing.T, filenames []string) ([]*ast.File, []error) {
	var files []*ast.File
	for _, filename := range filenames {
		file, err := parser.ParseFile(fset, filename, nil, parser.AllErrors)
		if file == nil {
			t.Fatalf("%s: %s", filename, err)
		}
		files = append(files, file)
	}
	runner := &AnalysisRunner{}
	runner.trieLimit = 2
	runner.efficiency = false
	goTest = true
	err := runner.Run(filenames)
	if err != nil {
		t.Fatal(err)
	}
	var raceErrors []error
	for _, msg := range runner.racyStackTops {
		raceErrors = append(raceErrors, fmt.Errorf(msg))
	}
	return files, raceErrors
}

func checkFile(t *testing.T, testfiles []string) {
	// parse files and collect parser errors
	logrus.Infof("Running %s", testfiles[0])

	files, errlist := runChecker(t, testfiles)

	if *listErrors && len(errlist) > 0 {
		t.Errorf("--- %s:", testfiles)
		for _, err := range errlist {
			t.Error(err)
		}
	}

	// typecheck and collect typechecker errors
	var conf types.Config
	conf.Importer = importer.Default()
	conf.Error = func(err error) {
		if *haltOnError {
			defer panic(err)
		}
		if *listErrors {
			t.Error(err)
			return
		}
	}

	if *listErrors {
		return
	}

	pkgName := "main"
	// match and eliminate errors;
	// we are expecting the following errors
	errmap := errMap(t, pkgName, files)

	marker := "."
	eliminated := eliminate(t, errmap, errlist)
	if !eliminated {
		marker = "+"
	}
	// there should be no expected errors left
	if len(errmap) > 0 {
		t.Errorf("--- %s: %d source positions with expected (but not reported) races:", pkgName, len(errmap))
		for pos, list := range errmap {
			for _, rx := range list {
				t.Errorf("Expected Race %q:\n  %s", rx, pos)
			}
		}
		if marker == "." {
			marker = "-"
		} else {
			marker += "-"
		}
	}
	if marker == "." {
		atomic.AddInt32(&passed, 1)
	}
	fmt.Printf("%40s %s\n", testfiles, marker)
}

func TestRace(t *testing.T) {
	testenv.MustHaveGoBuild(t)
	testMode = true
	if *parallel == 0 {
		*parallel = runtime.GOMAXPROCS(0)
	}

	logrus.Infof("Parallelism=%d", *parallel)
	logrus.SetLevel(logrus.FatalLevel)

	// If explicit test files are specified, only check those.
	if files := *testFiles; files != "" {
		checkFile(t, strings.Split(files, " "))
		return
	}

	// Otherwise, run all the tests in parallel.
	var (
		sem = semaphore.NewWeighted(int64(*parallel))
		ctx = context.Background()
		wg  = sync.WaitGroup{}
	)

	wg.Add(len(tests))
	for _, file := range tests {
		if err := sem.Acquire(ctx, 1); err != nil {
			logrus.Fatalf("Failed to acquire semaphore: %v", err)
		}
		go func(file string) {
			defer sem.Release(1)
			defer wg.Done()
			checkFile(t, []string{file})
		}(file)
	}

	wg.Wait()

	fmt.Printf("Passed %d/%d\n", passed, len(tests))
}
