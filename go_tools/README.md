# Go Tools -> Go Pointer Analysis 

Git clone from https://github.com/golang/tools

Start from commit 146a0deefdd11b942db7520f68c117335329271a

For any panic, please submit an issue with copy/paste crash stack. Thanks.

## How to Use?
Go to https://github.tamu.edu/April1989/go_pointeranlaysis/blob/master/main/main.go, check how to use the current callgraph and queries. 

*REMIND*: for the statement traversal in race detection, to obtain the callee target(s), it is safe to follow the callgraph instead of querying the points-to set of the receiver variable. 

## User APIs (for detector) 
```go
ResultWCtx:
GetMain() //return the main *cgnode
GetCGNodebyFunc(fn *ssa.Function) //return []*cgnode by *ssa.Function
PointsTo(v ssa.Value) //return the corresponding []PointerWCtx for ssa.Value, user does not need to distinguish different queries anymore
```

## kCFA
Stable version: checkout the newest commit 

### Main Changes
- Create k-callsite-sensitive contexts for static/invoke calls
- Generate constraints/cgnode online for invoke calls and targets when it is necessary
- Currently, skip the creation of reflection and dynamic calls due to the huge number

## Origin-sensitive
Stable version: checkout the newest commit 

### What is Origin? 
We treat a go routine instruction as an origin entry point, and all variables/function calls inside this go rountine share the same context as their belonging go routine.

### Regarding the Code/SSA IR
For origin-sensitive in Go, we have two cases (the following are IR instructions):
- Case 1: no ```make closure```, Go directly invokes a static function: e.g., 

```go Producer(t0, t1, t3)```

create a new context for it.

- Case 2: a go routine requires a ```make closure```: e.g., 

```t37 = make closure (*B).RunParallel$1 [t35, t29, t6, t0, t1] ```

```go t37() ``` 

the make closure has been treated with origin-sensitive and its origin context has been created earlier, here we find the çreated obj and use its context to use here.

- Case 3: no ```make closure```, Go directly invokes a virtual function: e.g., 

```go (*ccBalancerWrapper).watcher(t0)```

same as Case 1.


### Panic due to pointer.(*analysis).taggedValue()
- where: package github.com/pingcap/tidb/cmd/benchraw
- problem: t6 in github.com/pingcap/tidb/statistics.NewQueryFeedback(as the IR shown below) has no makeinterface IR statement
created after the object initialization. 
```
==== Generating constraints for cg1824201:github.com/pingcap/tidb/statistics.NewQueryFeedback@[0:shared contour; ], shared contour
# Name: github.com/pingcap/tidb/statistics.NewQueryFeedback
# Package: github.com/pingcap/tidb/statistics
func NewQueryFeedback(physicalID int64, hist *Histogram, expected int64, desc bool) *QueryFeedback:
0:                                                                entry P:0 S:2
...
5:                                                              if.done P:3 S:0
	t5 = phi [2: 1:int, 6: 1:int, 4: 0:int] #tp                         int
	t6 = new QueryFeedback (complit)                         *QueryFeedback
	t7 = &t6.PhysicalID [#0]                                         *int64
	t8 = &t6.Valid [#6]                                               *bool
	t9 = &t6.Tp [#2]                                                   *int
	t10 = &t6.Hist [#1]                                         **Histogram
	t11 = &t6.Expected [#4]                                          *int64
	t12 = &t6.desc [#7]                                               *bool
	*t7 = physicalID
	*t8 = true:bool
	*t9 = t5
	*t10 = t1
	*t11 = expected
	*t12 = desc
	return t6
```
When there is a method invoke with pts(t6) as the base variable, since the 
object is not tagged, go pointer analysis will panic. E.g., the call invoke n172159.Type1(n940498 ...) shown below:
```
==== Generating constraints for cg172157:(*github.com/modern-go/reflect2.safeType).AssignableTo@[0:shared contour; ], shared contour
# Name: (*github.com/modern-go/reflect2.safeType).AssignableTo
# Package: github.com/modern-go/reflect2
func (type2 *safeType) AssignableTo(anotherType Type) bool:
0:                                                                entry P:0 S:0
...
; t1 = invoke anotherType.Type1()
	create n940498 func() reflect.Type for invoke.targets
	create n940499 reflect.Type for invoke.results
	copy n940493 <- n940499
	invoke n172159.Type1(n940498 ...)
	invoke anotherType.Type1()@(*github.com/modern-go/reflect2.safeType).AssignableTo to targets n940498 from cg172157:(*github.com/modern-go/reflect2.safeType).AssignableTo@[0:shared contour; ]
```
- reason: 1. due to the return type of github.com/pingcap/tidb/statistics.NewQueryFeedback is a pointer, no make interface
IR stmt generated; 2. the panic constraint is from reflection, which will not consider to create make interface stmt.  

- solution: 1. we identify this reflect pkg and skip it analysis; 2. we do something similar below, but should not be like this.
```
==== Generating constraints for cg1190:(*go.etcd.io/etcd/client.httpMembersAPI).Remove@[0:shared contour; ], shared contour
# Name: (*go.etcd.io/etcd/client.httpMembersAPI).Remove
# Package: go.etcd.io/etcd/client
# Location: /Users/bozhen/go/pkg/mod/go.etcd.io/etcd@v0.5.0-alpha.5.0.20200824191128-ae9734ed278b/client/members.go:195:26
func (m *httpMembersAPI) Remove(ctx context.Context, memberID string) error:
0:                                                                entry P:0 S:2
	t0 = new membersAPIActionRemove (complit)       *membersAPIActionRemove
	t1 = &t0.memberID [#0]                                          *string
	*t1 = memberID
	t2 = &m.client [#0]                                         *httpClient
	t3 = *t2                                                     httpClient
	t4 = make httpAction <- *membersAPIActionRemove (t0)         httpAction
	t5 = invoke t3.Do(ctx, t4)          (*net/http.Response, []byte, error)
	t6 = extract t5 #0                                   *net/http.Response
	t7 = extract t5 #1                                               []byte
	t8 = extract t5 #2                                                error
	t9 = t8 != nil:error                                               bool
	if t9 goto 1 else 2
```