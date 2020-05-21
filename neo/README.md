# Neo-go: Dynamic Order Violation Prediction

Neo can predict general order violation bugs such as:
- sending to a closed chan
- missed signal on sync.Cond
- add after wait on waitGroup
- operations to file descriptors
- (C/C++ only) concurrent use-after-free

## Try Neo

The fastest way to try Neo is to use the docker image provided as [Dockerfile](./Dockerfile) in the current folder.
To build and run the image in one command,
```
docker run -it --entry-point bash $(docker build -q .)
```

The container has several built-in commands:
- `show_cur_race`: show which race detector library is currently linked to Go compiler.
- `switch_race <tsan|neo>`: switch the race library's symlink to Tsan or Neo. Neo is linked by default.
- `test_race`: Run all tests for tsan or neo

The test file `neo_test.go` contains a set of tests specifically for neo, including high-level race conditions over channels, `sync.Cond`, `sync.Waitgroup`, etc.
```
go test -v -race neo_test.go
```

Test names starting with `Race`, `OV`, `Deadlock` contain races, order violation bugs, deadlocks, resp.

Test names starting with `NoRace`, `NoOV`, `NoDeadlock` are free from concurrency bugs. 
They are used to check NEO does not report false positives on them. 

### Run Neo in real-world projects 

To try the tool on the tests from Kubernetes (k8s.io):
```
git clone --depth 1 https://github.com/kubernetes/kubernetes.git
cd kubernetes

# run one test
go test -race -run <TestName> ./... 

# run all tests. May take a while. Change timeout accordingly.
go test -race -timeout 30s ./... 
```

## Annotations

Users can add custom annotations to check order violations on a pair of operations to the same address.
Annotations are currently exposed as low-level calls to Neo runtime:
- race.Read(addr)
- race.ReadNoAcq(addr)
- race.ReadAtomicNoAcq(addr)
- race.ReadAtomic(addr)
- race.Write(addr)
- race.WriteNoAcq(addr)
- race.WriteAtomic(addr)
- race.WriteAtomicNoAcq(addr)

TODO(yahui): explain how to use the annotations.
Maybe consider using more user-friendly names.

## Caveats

__Although Neo can flag plain data races by default, the data race reports may contain (very few) false positives.__

__Neo is intended for order violation detection rather than data race detection.__

Neo is formally sound for order violation detection but unsound for race detection.
Neo implements the weakly-doesn't-commute partial order (WDC, see [Roemer el. al.](https://arxiv.org/pdf/1905.00494.pdf)) and a vindication algorithm that is different from DC.


## Source code

Neo is packed into a customized Go: https://github.com/dopelsunce/go-pred.
The runtime library is developed on top of TSAN (see the neo-ufo repo) and the pre-built binary (race_darwin_amd64.syso, race_linux_amd64.syso) is included in the Neo runtime.

## Install from source

Same as installing Go from source.
Download the source code and follow this guide: https://golang.org/doc/install/source.



