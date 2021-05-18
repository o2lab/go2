# Go Tools -> Go Pointer Analysis

## This branch is for call back funcs shown in app func but called after several level of lib calls

We want to skip the analysis of lib calls, too expensive. If we are going to create synthetic ssa for lib functions,
   we start here.
   1. write a native.xml file with all irs we want
   2. preload all irs in native.xml
   3. when reach here @ssa/create.go CreatePackage(), we check if it is in preload, if yes, use this synthetic

Key files:
 ssa/create.go
 ssa/builder.go
 
Standard libraries: https://pkg.go.dev/std

#### *Update*


====================================================================================

Git clone from https://github.com/golang/tools, start from commit 146a0deefdd11b942db7520f68c117335329271a (around v0.5.0-pre1).

The default go pointer analysis algorithm (v0.5.0-pre1) is at ```go_tools/go/pointer_default```.

For any panic, please submit an issue with copy/paste crash stack. Thanks.

## How to Use?
Go to ```go_tools/main```, and run ```go build```. Then, run ```./main``` with the following flags and 
the directory of the go project that you want to analyze.
It will go through all of your main files and analyze them one by one.

#### Flags
- *path*: default value = "", Designated project filepath. 
- *doLog*: default value = false, Do a log to record all procedure, so verbose. 
- *doCompare*: default value = false, Do comparison with default pta about performance and result.

For example,
 
```./main -doLog -doCompare ../grpc-go/benchmark/server```

This will run the origin-sensitive pointer analysis on all main files under directory ```../grpc-go/benchmark/server```,
as well as generate a full log and a comparison with the default algorithm about performance and result.

*Note* that ```-doLog``` is very verbose and significantly slowdown the analysis.

## User APIs (for detector) 
Go to https://github.com/april1989/origin-go-tools/main/main.go, check how to use the callgraph and queries. 

## Origin-sensitive

#### What is Origin? 
We treat a go routine instruction as an origin entry point, and all variables/function calls inside this go rountine share the same context as their belonging go routine.

#### Main Changes from Default
Instead of pre-computing all cgnodes and their constraints before actually propagating changes among points-to constraints,
we start from the reachable cgnodes ```init``` and ```main``` and gradually compute reachable cgnodes and their constraints. 

## kCFA

#### Main Changes from Default
- Create k-callsite-sensitive contexts for static/invoke calls
- Generate constraints/cgnode online for invoke calls and targets when it is necessary
- Currently, skip the creation of reflection and dynamic calls due to the huge number


========================================================================
## Doc of Default Algorithm

The most recent doc is https://pkg.go.dev/golang.org/x/tools/go/pointer#pkg-overview, quoted:

"SOUNDNESS

The analysis is fully sound when invoked on pure Go programs that do not use reflection or unsafe.Pointer conversions. In other words, if there is any possible execution of the program in which pointer P may point to object O, the analysis will report that fact."

However, over soundness is unnecessary. 

========================================================================
## Major differences between the results of mine and default

#### 1. Queries and Points-to Set (pts)
All example below based on race_checker/tests/cg.go

#### Why my query has two pointers for one ssa.Value:
There are two pointers involved in one constraint, both of which are stored under the same ssa.Value in my queries.

#### Why default query is empty but mines is not:
Default has tracked less types than mine, of which constraints and invoke calls are missing in the default result.
Hence, it has empty pts while mine has non-empty pts. For example,
```
SSA:  &t57[41:int]
My Query: (#obj:  1 )
   n597&[0:shared contour; ] : [slicelit[*]]
   n2266&[0:shared contour; ] : []
Default Query: (#obj:  0 )
   n36563 : []

In default log:
; t181 = &t57[41:int]
	localobj[t181] = n37034
	type not tracked: *strconv.leftCheat

In my log:
; t181 = &t57[41:int]
	localobj[t181] = n2266
	addr n597 <- {&n2266}
```

#### Why my query is empty but default is not:
Due to the default algorithm (pre-compute all constraints for all functions),
it generates a lot of unreachable functions/cgnodes (they have no callers), as well as their constraints.
This also affect the pts of the reachable part in cg and pts, since they may be polluted.
For example,
```
SSA:  (*internal/reflectlite.rtype).Size  //-> (*internal/reflectlite.rtype).Size is not reachable function
My Query: (#obj:  0 )
   n8971&(Global/Local) : []
Default Query: (#obj:  1 )
   n6354 : [(*internal/reflectlite.rtype).Size]
```

#### Why my query is non-empty but no corresponding pointer in default:
Default does not create queries for those types (not tracked types).
For example,
```
654.
SSA:  io.ErrClosedPipe
My Query: (#obj:  1 )
   n4448&(Global/Local) : [makeinterface:*errors.errorString]
Default Query: nil)

In default log:
; *ErrClosedPipe = t10
	copy n19413 <- n39471

In my log:
; *ErrClosedPipe = t10
	create n4448 error for global
	globalobj[io.ErrClosedPipe] = n4448
	copy n4448 <- n4431
```


#### Why default query is empty but no corresponding pointer in mine:
IDK.
For example,
```
SSA:  &r.peekRune [#4]
My Query: nil
Default Query: (#obj:  0 )
   n44378 : []
and
SSA:  ssa:wrapnilchk(v, "internal/reflectl...":string, "IsNil":string)
My Query: nil)
Default Query: (#obj:  0 )
   n44378 : []
and
	val[t115] = n44377  (*ssa.FieldAddr)
	create n44378 *[16]byte for query
	copy n44378 <- n44377
```

#### Why my query has less objs in pts than the default:
All missing objs in my pts are due to objs and constraints introduced by unreachable functions.
This is the pollution we mentioned before.
For example,
```
SSA:  *t49
My Query: (#obj:  27 )
   n11815&[0:shared contour; ] : [makeinterface:int makeinterface:[]int makeinterface:int makeinterface:*internal/reflectlite.ValueError makeinterface:*internal/reflectlite.ValueError makeinterface:string makeinterface:string makeinterface:*internal/reflectlite.ValueError makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string]
Default Query: (#obj:  79 )
   n18076 : [makeinterface:int makeinterface:[]int makeinterface:string makeinterface:*internal/reflectlite.ValueError makeinterface:*internal/reflectlite.ValueError makeinterface:*internal/reflectlite.ValueError makeinterface:*internal/reflectlite.ValueError makeinterface:string makeinterface:*internal/reflectlite.ValueError makeinterface:string makeinterface:string makeinterface:string makeinterface:fmt.scanError makeinterface:fmt.scanError makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:*internal/reflectlite.ValueError makeinterface:string makeinterface:string makeinterface:*internal/reflectlite.ValueError makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:int makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:*internal/reflectlite.ValueError makeinterface:fmt.scanError makeinterface:fmt.scanError makeinterface:fmt.scanError makeinterface:fmt.scanError makeinterface:fmt.scanError makeinterface:fmt.scanError makeinterface:fmt.scanError makeinterface:fmt.scanError makeinterface:fmt.scanError makeinterface:fmt.scanError makeinterface:fmt.scanError makeinterface:fmt.scanError makeinterface:fmt.scanError makeinterface:fmt.scanError makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:*errors.errorString makeinterface:string makeinterface:string makeinterface:*internal/reflectlite.ValueError makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string makeinterface:string]
```


#### 2. CG

#### *Why are the cgs from default and my pta different?*

The default algorithm create cgnodes for functions that are not reachable from the main entry.
For example, when analyzing the main entry ```google.golang.org/grpc/benchmark/server```,
the default algorithm pre-generate constraints and cgnodes for function:
```go
(*google.golang.org/grpc/credentials.tlsCreds).ServerHandshake
``` 
which is not reachable from the main entry (it has no caller in cg).

This can be reflected in the default analysis data: 
``` 
Call Graph: (function based) 
#Nodes:  14740
#Edges:  45550
#Unreach Nodes:  7698
#Reach Nodes:  7042
#Unreach Functions:  7698
#Reach Functions:  7042

Done  -- PTA/CG Build; Using  5m13.058385739s . 
```
Default generates 14740 functions and their constraints, however, only 7042 (at most) of them can be reachable from the main.

While my analysis data is:
```
Call Graph: (cgnode based: function + context) 
#Nodes:  6306
#Edges:  23291
#Unreach Nodes:  39
#Reach Nodes:  6267
#Unreach Functions:  39
#Reach Functions:  5870

#Unreach Nodes from Pre-Gen Nodes:  39
#Unreach Functions from Pre-Gen Nodes:  39
#(Pre-Gen are created for reflections)

Done  -- PTA/CG Build; Using 10.279403083s. 
```
My analysis traverse 6267 functions that can be reached after extended the traced types.

This not only introduce differences in cg, but also unreachable constraints and objs, which can be 
propagated to the cgnodes and constraints that can be reached from the main entry. This causes false 
call edges and callees in default cg.

Most CG DIFFs from comparing mine with default result are due to this reason. 


#### Why the unreachable function/cgnode will be generated?
This is because the default algorithm creates nodes and constraints for all methods of all types
that are dynamically accessible via reflection or interfaces (no matter it will be reached or not).


#### Why the cgnodes from default not include some callees as mine?
Because default algo has less type tracked than mine (no constraints generated for them and hence
no propagation), Hence, some invoke calls has no base instance that will exist if we track those types.
Consequently, no callee functions/cgs generated as well as constraints.
   
 

