package _tests_callback

import (
	"fmt"
	"github.com/april1989/origin-go-tools/go/loader"
	"github.com/april1989/origin-go-tools/go/myutil"
	"github.com/april1989/origin-go-tools/go/myutil/flags"
	"github.com/april1989/origin-go-tools/go/pointer"
	"github.com/april1989/origin-go-tools/go/ssa"
	"github.com/april1989/origin-go-tools/go/ssa/ssautil"
	"go/ast"
	"go/parser"
	"go/token"
	"io/ioutil"
	"strconv"
	"strings"
	"testing"
)

var inputs = []string{
	"testcases/cb_long.go",
	//"testcases/cb_namefn.go",
	//"testcases/cb_typefn.go",

	"testcases/Grpc/1862/main.go",
	"testcases/Istio/8144/main.go",
}

type expectPTS struct {
	lhs *loc //lhs
	rhs *loc //rhs
}

type loc struct {
	name string //lhs
	fn   string
	pos  token.Pos
}

func (e *expectPTS) String() string {
	return e.lhs.name + "@" + e.lhs.fn + "(" + strconv.Itoa(int(e.lhs.pos)) + "); " +
		e.rhs.name + "@" + e.rhs.fn + "(" + strconv.Itoa(int(e.rhs.pos)) + ")"
}

func expectation(f *ast.File) []*expectPTS {
	var expected_r []*expectPTS
	for _, c := range f.Comments {
		text := strings.TrimSpace(c.Text())
		if t := strings.TrimPrefix(text, "@pointsto "); t != text {
			if c.Pos() == token.NoPos {
				return nil
			}

			e := &expectPTS{}
			sets := strings.Split(t, "=")
			for i, set := range sets {
				idx := strings.Index(set, "@")
				if i == 0 {
					e.lhs = &loc{
						name: set[0:idx],
						fn:   set[idx+1:],
						pos:  c.Pos(),
					}
				} else if i == 1 {
					e.rhs = &loc{
						name: set[0:idx],
						fn:   set[idx+1:],
						pos:  c.Pos(),
					}
				}
			}
			expected_r = append(expected_r, e)
		}
	}
	return expected_r
}

//bz: verify the correctness of using callback on benchmarks from _tests_callback
func TestCallback(t *testing.T) {
	myutil.InitialTest()
	for i, filename := range inputs {
		fmt.Println("\n--- testing: ", filename)
		content, err := ioutil.ReadFile(filename)
		if err != nil {
			t.Errorf("couldn't read file '%s': %s", filename, err)
			continue
		}

		conf := loader.Config{
			ParserMode: parser.ParseComments,
		}
		f, err := conf.ParseFile(filename, content)
		if err != nil {
			t.Error(err)
			continue
		}

		want := expectation(f)
		if want == nil {
			t.Errorf("No @pointsto: comment in %s", filename)
		}

		conf.CreateFromFiles("main", f)
		iprog, err := conf.Load()
		if err != nil {
			t.Error(err)
			continue
		}

		prog := ssautil.CreateProgram(iprog, 0)
		mainPkg := prog.Package(iprog.Created[0].Pkg)
		prog.Build()

		//run pta
		flags.DoLog = true
		flags.DoCallback = true
		flags.DoLevel = 1
		myutil.DoEachMainMy(i, mainPkg)
		result := pointer.GetMain2ResultWCtx()[mainPkg]

		//verify
		if got := verifyPTS(result, want); got != nil {
			for expect, get := range got {
				t.Errorf("\nwant:\n%s\ngot:\n%s; %s", expect, get[0].PointsTo().String(), get[1].PointsTo().String())
			}
		}
	}
}

func verifyPTS(result *pointer.ResultWCtx, want []*expectPTS) map[*expectPTS][]pointer.PointerWCtx {
	ret := make(map[*expectPTS][]pointer.PointerWCtx)
	//check
	for _, exp := range want {
		lhsV := retrieveV(result, exp.lhs)
		rhsV := retrieveV(result, exp.rhs)
		if lhsV == nil || rhsV == nil {
			panic("nil lhsV/rhsV. ")
		}

		// should only be one pts in these test cases
		lhss := result.PointsTo(lhsV)
		rhss := result.PointsTo(rhsV)
		if len(lhss) == 0 && len(rhss) == 0 { //both empty
			//set := make([]pointer.PointerWCtx, 2)
			//set[0] = pointer.PointerWCtx{}
			//set[1] = pointer.PointerWCtx{}
			//ret[exp] = set
			continue
		} else if len(lhss) == 0 { //not the same
			set := make([]pointer.PointerWCtx, 2)
			set[0] = pointer.PointerWCtx{}
			set[1] = rhss[0]
			ret[exp] = set
			continue
		} else if len(rhss) == 0 { //not the same
			set := make([]pointer.PointerWCtx, 2)
			set[0] = lhss[0]
			set[1] = pointer.PointerWCtx{}
			ret[exp] = set
			continue
		}

		lhsPTS := lhss[0]
		rhsPTS := rhss[0]

		if lhsPTS.MayAlias(rhsPTS) {
			continue
		} else {
			set := make([]pointer.PointerWCtx, 2)
			set[0] = lhsPTS
			set[1] = rhsPTS
			ret[exp] = set
		}
	}

	return ret
}

//bz: nil if not exist in localv
func retrieveV(result *pointer.ResultWCtx, loc *loc) ssa.Value {
	cg := result.CallGraph
	for fn, cgns := range cg.Fn2CGNode { // should only be one cgn in these test cases
		if fn.String() == loc.fn {
			cgn := cgns[0]
			for v, _ := range cgn.Getlocalval() {
				if v.Name() == loc.name {
					//bz: when write the comment: use the lhs of ir, e.g.,
					// t0 = &c.callbacks [#0]
					// write as: t0@(...).fn
					return v
				}
			}
			//check if in free var of fn
			for _, freeV := range fn.FreeVars {
				if freeV.Name() == loc.name {
					return freeV
				}
			}
		}
	}
	return nil
}
