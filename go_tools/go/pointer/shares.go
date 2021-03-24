package pointer

import "github.tamu.edu/April1989/go_tools/go/ssa"

//bz:
// i want to share func/cgnode/enclosing pointers, objs and constraints
// that are created with context @[0:shared contour; ]; since they are frequently
// called by different mains in the same pkg
// TODO: What to do for renumbering ? this cannot be skipped for performance ...
// TODO: I also want to borrow their pts if possible, how?

// Initial Solution: Solving is the slowest part. => we do diff propagation among differnt ptas
//  1 see a func creation in makeFunctionObject()
//  2 find the corresponding function/cgnode and its enclosing constraints in share.go from previous pta
//  3 if all founds CAN BE APPLIED to the new pta, copy the solve (struct with prePTS, pts, complex, etc) to the corresponding node.
//  3 skip the solving of all nodes in new pta.
//  -> no renumbering problem: we copy before renumber, TODO: do we need to handle the delta in solver?
//  -> we can borrow its pts
//  TODO: memory consuming should be the same ...
//  TODO: when CAN BE APPLIED across different pta?
//       for a specific callee in this pool, if we compare its callers with pts(actual param),
//       if no diff with the info we stored, we can borrow pts and call chains inside this callee.
//       if later new callers appear, we can do diff ? but if we do diff, we will mess up existing pts.
//       Besides, how to confirm obj field access in this func have the same pts?

var (
	fn2obj = make(map[*ssa.Function] *FnSummary) //map function <-> function summary

)

//a summary of fn
type FnSummary struct {
	obj             nodeid  //return val of makeFunctionObject()
	fn              *ssa.Function //who i am for
	constraints     []constraint  //enclosing constraints
}

//bz: can we apply everything in summary to another analysis?
func (sum *FnSummary) CanApply(another *analysis) bool {
	return false
}


//is this func in our share map > fn2obj?
func IsShared(fn *ssa.Function) bool {
	return fn2obj[fn] != nil
}

//create summary for fn
func CreateSumForFunc(fn *ssa.Function, obj nodeid, c []constraint) {
	assert(fn2obj[fn] == nil, "Summary exists: " + fn.String())
	fn2obj[fn] = &FnSummary{
		obj: obj,
		fn:  fn,
		constraints: c,
	}
}


func GetSumForFunc(fn *ssa.Function) *FnSummary {
	return fn2obj[fn]
}





