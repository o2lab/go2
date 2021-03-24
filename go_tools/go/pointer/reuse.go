package pointer

import "github.tamu.edu/April1989/go_tools/go/ssa"

/*
   bz: used when two cgnodes with the same function and context (must be shared contour) are same across different mains,
   including pts created in this fn, call edges, etc. Then we copy solved state to unsolved.
*/

var (
	analyses []*analysis //bz: utilized by AnalyzeMultiMains(), to store computed *analysis to save solving time
)

//bz: see above
func reuse(a *analysis) {
	//the ones that can be reused (shared contour) only shown in a.globalobj
	for val, obj := range a.globalobj {
		if fn, ok := val.(*ssa.Function); ok {
			cgn := a.nodes[obj].obj.cgn //my cgn
			//see existing analyses
			for _, othera := range analyses {
				oobj := othera.globalobj[fn]
				if oobj == 0 {
					continue //no such fn in this analysis
				}

				//check if can be applied here
				ocgn := othera.nodes[oobj].obj.cgn //other cgn
				if canApply(cgn, ocgn, a, othera) {
					apply(cgn, ocgn)
					return
				}
			}
		}
	}

}

//bz: check if we can copy all from ocgn to cgn
//TODO: what is the rules to check here ?
//  tmp solution: if cgn.callers \in ocgn.callers -> true
func canApply(cgn *cgnode, ocgn *cgnode, a *analysis, oa *analysis) bool {
	//oNode := oa.result.CallGraph.GetNodeWCtx(ocgn)
	//return oNode != nil
	return true
}

//bz: copy all from ocgn to cgn; this is after renumbering
//TODO: how can we copy ? idk the corresponding nodeid in o's pts
//  do we in real copy?
//  OR we use a reference to map this relation hidden in our code ? and remove the nodeid(s) from a's solver
func apply(cgn *cgnode, ocgn *cgnode) {

}
