# Go race detector

![Tests](https://github.com/o2lab/go2/workflows/Tests/badge.svg)

## Build instructions

Prerequisites: Go 1.2+

First, build the checker in the current folder.
Dependencies will be automatically downloaded.
```
go build
```
By default, the built artifact is named `race-checker`.
Usage:

```
./race-checker [options] <path>
```

`<path>` must lead to main.go file in the main package.

Supported options:

- `-collectStats`: Show a report of analysis statistics.
- `-debug`: Show debug information.
- `-help`: Show all command-line options.
- `-withComm` or `withoutComm`: Run the analysis with / without consideration of channel communication. To be used when running real programs. 
- `-path=`: For specifying the path from which packages are to be analyzed. For example, `-path=github.com/pingcap/tidb` would consider packages from the TiDB repo. 


### Installation instructions

To install the race-checker in the PATH,
```
go install
```

To test if the binary is installed correctly and show all the options,
```
race-checker -help
```

### Running tests

To run all the end-to-end tests (file list in `race_test.go`),
```
go test
``` 

The above command runs the tests concurrently. 
The number of workers equals to the number of cores available by default.
To set the number of cores to use explicitly, use the `-cpu` option.

Run `go test` in `race_checker` folder to run all tests.
To run individual end-to-end tests,
```
go test -files <test_file>
```

### Example

Try some test cases in adopted micro-benchmarks:

```
./race-checker GoBench/Kubernetes/81091/main.go
./race-checker godel2/ch-as-lock-race/main.go
```

positive race result would be shown as follows, 
![Image of data race report](tests/screenshot.png)

### Real-world benchmarks

A catalogue of real-world programs to be tested on: 
https://docs.google.com/spreadsheets/d/1XQznzDadxw9Tp6SVOCBndSTy3J65N1CscJUBRZwrcTU/edit?usp=sharing
