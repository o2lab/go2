package compare

//import (
//	"bufio"
//	"fmt"
//	"github.tamu.edu/April1989/go_tools/go/pointer"
//	"github.tamu.edu/April1989/go_tools/go/ssa"
//	"io"
//	"os"
//	"strings"
//)
//
//var default_cg map[string][]string
//var default_queries map[string][]string
//var default_in_queries map[string][]string
//
//var cg_diffs []*Diff
//var q_diffs []*Diff
//
//
////bz: difference between two pta results
//type Diff struct {
//	default_map   map[string][]string
//	my_map        map[string][]string
//}
//
//func (diff *Diff) print()  {
//	default_map := diff.default_map
//	my_map := diff.my_map
//	fmt.Println("My CG: ")
//	printMap(my_map)
//	fmt.Println("Default CG: ")
//	printMap(default_map)
//}
//
//func printMap(_map map[string][]string) {
//	for key, vals := range _map {
//		fmt.Println("  ", key)
//		for _, val := range vals {
//			fmt.Println("\t -> ", val)
//		}
//	}
//}
//
////bz: assume the two files from default pta is available -> now compare
//func Compare2(mainPkg string, result *pointer.ResultWCtx) {
//	fmt.Println("\n\nCompare ... ", mainPkg)
//
//	default_cg = make(map[string][]string)
//	default_queries = make(map[string][]string)
//	default_in_queries = make(map[string][]string)
//
//	//fill in the above maps
//	readFileForCG("/Users/Bozhen/Documents/GO2/tools/_logs/cg_" + mainPkg)
//	readFileForQueries("/Users/Bozhen/Documents/GO2/tools/_logs/query_" + mainPkg)
//
//	compareCG(result.CallGraph)
//
//	//output all cg diff
//	if len(cg_diffs) > 0 {
//		fmt.Println("CG DIFFs: ")
//		for i, cg_diff := range cg_diffs {
//			fmt.Print(i, ".")
//			cg_diff.print()
//		}
//	}
//
//	compareQueries(result.Queries)
//	compareInQueries(result.IndirectQueries)
//
//	//output all query/indirect query diff
//	if len(q_diffs) > 0 {
//		fmt.Println("QUERIES/INDIRECT QUERIES DIFFs: ")
//		for i, q_diff := range q_diffs {
//			fmt.Print(i, ".")
//			q_diff.print()
//		}
//	}
//
//	if len(q_diffs) == 0 && len(cg_diffs) == 0 {
//		fmt.Println("Done ... NO DIFF BETWEEN DEFAULT AND MINE. ")
//	}
//}
//
////bz: if exist, return its val; otherwise return nil
//func existKey(default_map map[string][]string, myval string) []string {
//	if default_val, ok := default_map[myval]; ok {
//		return default_val
//	}
//	return nil
//}
//
////bz: if exist such val in default values
//func existVal(default_val []string, myval string) bool {
//	for _, val := range default_val {
//		if strings.EqualFold(val, myval) {
//			return true
//		}
//	}
//	return false
//}
//
//func createDiffForCG(caller *pointer.Node, default_val []string)  {
//	mycaller := caller.GetFunc().String()
//	my_map := make(map[string][]string)
//	var my_val []string
//	for _, out := range caller.Out {   //my callees with ctx
//		mycallee := out.Callee.GetFunc().String()
//		my_val = append(my_val, mycallee)
//	}
//	my_map[mycaller] = my_val
//
//	var diff *Diff
//	if default_val == nil {
//		diff = &Diff{
//			default_map: nil,
//			my_map: my_map,
//		}
//	}else{
//		default_map := make(map[string][]string)
//		default_map[mycaller] = default_val
//
//		diff = &Diff{
//			default_map: default_map,
//			my_map: my_map,
//		}
//	}
//
//	cg_diffs = append(cg_diffs, diff)
//}
//
//
//func createDiffForQuery(ssa_val string, default_pts []string, my_pts []string) {
//	my_map := make(map[string][]string)
//	my_map[ssa_val] = my_pts
//
//	var diff *Diff
//	if default_pts == nil {
//		diff = &Diff{
//			default_map: nil,
//			my_map: my_map,
//		}
//	}else{
//		default_map := make(map[string][]string)
//		default_map[ssa_val] = default_pts
//
//		diff = &Diff{
//			default_map: default_map,
//			my_map: my_map,
//		}
//	}
//
//	q_diffs = append(q_diffs, diff)
//}
//
//
//func compareCG(cg *pointer.GraphWCtx) {
//	callers := cg.Nodes
//	for _, caller := range callers { // my caller with ctx
//		mycaller := caller.GetFunc().String()
//		default_val := existKey(default_cg, mycaller)
//		if default_val == nil {
//			//no such key in default
//			createDiffForCG(caller, default_val)
//			continue
//		}
//
//		hasDiff := false
//		outs := caller.Out           // caller --> callee
//		for _, out := range outs {   //my callees with ctx
//			mycallee := out.Callee.GetFunc().String()
//			if existVal(default_val, mycallee) {
//				continue
//			}else{
//				//no such val in default
//				hasDiff = true
//			}
//		}
//
//		if hasDiff {
//			createDiffForCG(caller, default_val)
//		}
//	}
//}
//
//func compareQueries(queries map[ssa.Value][]pointer.PointerWCtx) {
//	for v, ps := range queries {
//		ssa_val := v.String()
//		//bz: we need to do like default: merge them to a canonical node
//		var my_pts []string
//		for _, p := range ps { //p -> types.Pointer: includes its context; SSA here is your *ssa.Value
//			_pts := p.PointsTo().String()
//			_pts = _pts[1:len(_pts) - 1]
//			objs := strings.Split(_pts, ",")
//			for _, obj := range objs {
//				my_pts = append(my_pts, obj)
//			}
//		}
//
//		default_pts := existKey(default_queries, ssa_val)
//		if default_pts == nil {
//			//no such key in default
//			createDiffForQuery(ssa_val, default_pts, my_pts)
//			continue
//		}
//
//		hasDiff := false
//		for _, my_obj := range my_pts {
//			if existVal(default_pts, my_obj) {
//				continue
//			}else{
//				//no such val in default
//				hasDiff = true
//			}
//		}
//
//		if hasDiff {
//			createDiffForQuery(ssa_val, default_pts, my_pts)
//		}
//	}
//}
//
//func compareInQueries(queries map[ssa.Value][]pointer.PointerWCtx) {
//	for v, ps := range queries {
//		ssa_val := v.String()
//		//bz: we need to do like default: merge them to a canonical node
//		var my_pts []string
//		for _, p := range ps { //p -> types.Pointer: includes its context; SSA here is your *ssa.Value
//			_pts := p.PointsTo().String()
//			_pts = _pts[1:len(_pts) - 1]
//			objs := strings.Split(_pts, ",")
//			for _, obj := range objs {
//				my_pts = append(my_pts, obj)
//			}
//		}
//
//		default_pts := existKey(default_in_queries, ssa_val)
//		if default_pts == nil {
//			//no such key in default
//			createDiffForQuery(ssa_val, default_pts, my_pts)
//			continue
//		}
//
//		hasDiff := false
//		for _, my_obj := range my_pts {
//			if existVal(default_pts, my_obj) {
//				continue
//			}else{
//				//no such val in default
//				hasDiff = true
//			}
//		}
//
//		if hasDiff {
//			createDiffForQuery(ssa_val, default_pts, my_pts)
//		}
//	}
//}
//
////bz: read file part from https://stackoverflow.com/questions/8757389/reading-a-file-line-by-line-in-go
//func readFileForCG(fn string) {
//	fmt.Println("Read File ... ", fn)
//
//	file, err := os.Open(fn)
//	if err != nil {
//		panic(fmt.Sprintln(err))
//	}
//	defer file.Close()
//
//	var key string
//	var val []string
//
//	// Start reading from the file with a reader.
//	reader := bufio.NewReader(file)
//	var line string
//	for {
//		line, err = reader.ReadString('\n')
//		if err != nil && err != io.EOF {
//			break
//		}
//
//		// Process the line here.
//		if strings.HasPrefix(line, "n") {
//			if key != "" {
//				default_cg[key] = val
//			}
//			//update
//			key = line[strings.Index(line, ":") + 1:len(line) - 1] //new key
//			val = make([]string, 0)
//		} else if strings.HasPrefix(line, "  ->") {
//			target := line[strings.Index(line, ":") + 1:len(line) - 1]
//			val = append(val, target)
//		} else {
//			//end of file
//		}
//
//		if err != nil {
//			break
//		}
//	}
//	if err != io.EOF {
//		panic(fmt.Sprintln(" >>> Failed with error: %v\n", err))
//	}
//}
//
//func readFileForQueries(fn string) {
//	fmt.Println("Read File ... ", fn)
//
//	file, err := os.Open(fn)
//	if err != nil {
//		panic(fmt.Sprintln(err))
//	}
//	defer file.Close()
//
//	var queries map[string][]string
//
//	// Start reading from the file with a reader.
//	reader := bufio.NewReader(file)
//	var line string
//	for {
//		line, err = reader.ReadString('\n')
//		if err != nil && err != io.EOF {
//			break
//		}
//
//		// Process the line here.
//		if strings.HasPrefix(line, "(") {
//			ssa_val := line[1:strings.Index(line, "):")]
//			pts := line[strings.Index(line, "{[") + 2: len(line) - 3]
//			objs := strings.Split(pts, ",")
//			if len(objs) == 1 && objs[0] == "" { //empty pts
//				queries[ssa_val] = make([]string, 0)
//				continue
//			}
//			queries[ssa_val] = objs
//		} else if strings.Contains(line, "Queries Detail:") {
//			queries = default_queries
//		} else if strings.Contains(line, "Indirect Queries Detail:") {
//			queries = default_in_queries
//		}
//
//		if err != nil {
//			break
//		}
//	}
//	if err != io.EOF {
//		panic(fmt.Sprintln(" >>> Failed with error: %v\n", err))
//	}
//}
