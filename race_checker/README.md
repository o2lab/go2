## Instructions

Prerequisites: Go 1.2+

First, build the checker in the current folder.
Dependencies will be automatically downloaded.

```
go build
```

The built binary by default is named `race-checker`.
Usage:

```
./race-checker [-debug] [-ptrAnalysis] <path>
```

`<path>` must lead to main.go file in the main package.

### Running tests

Run `go test` in the current folder.

### Flags

`-debug`: Show debug information.

`-ptrAnalysis`: Show occasions of pointer analysis returning multiple targets. Used for debugging purposes only. 

### Example

Try some test cases in adopted micro-benchmarks:

```
./race-checker GoBench/Kubernetes/81091/main.go
./race-checker godel2/ch-as-lock-race/main.go
```

Or make use of the makefile:

```
make
```
then
```
make runGoBench
```
positive race result would be shown as follows, 
![Image of data race report](/tests/screenshot.png)

A catalogue of real-world programs to be tested on: 
https://docs.google.com/spreadsheets/d/1XQznzDadxw9Tp6SVOCBndSTy3J65N1CscJUBRZwrcTU/edit?usp=sharing
