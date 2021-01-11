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
