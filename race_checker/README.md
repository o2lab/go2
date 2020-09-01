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
./race-checker [-debug] [-focus <coma-separated list of packages>] <path>
```

`<path>` can be a file or a folder that must be the main package.

### Flags

`-debug`: Show debug information.

`-focus`: Limit the analysis scope to a list of packages, separated by comas.

`-ptrAnalysis`: Show occasions of pointer analysis returning multiple targets. Used for debugging purposes only. 

### Example

Try some tests in the `tests` folder:

```
./race-checker tests/race_example1.go
./race-checker tests/GoBench/GoBench_Etcd.go
```

