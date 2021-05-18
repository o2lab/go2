package packages

import (
	"fmt"
	"os"
	"sort"
)

// Visit visits all the packages in the import graph whose roots are
// pkgs, calling the optional pre function the first time each package
// is encountered (preorder), and the optional post function after a
// package's dependencies have been visited (postorder).
// The boolean result of pre(pkg) determines whether
// the imports of package pkg are visited.
func Visit(pkgs []*Package, pre func(*Package) bool, post func(*Package)) {
	seen := make(map[*Package]bool)
	var visit func(*Package)
	visit = func(pkg *Package) {
		if !seen[pkg] {
			seen[pkg] = true

			if pre == nil || pre(pkg) {
				if pkg == nil {
					return //bz: added to bypass
				}
				paths := make([]string, 0, len(pkg.Imports))
				for path := range pkg.Imports {
					paths = append(paths, path)
				}
				sort.Strings(paths) // Imports is a map, this makes visit stable
				for _, path := range paths {
					visit(pkg.Imports[path])
				}
			}

			if post != nil {
				post(pkg)
			}
		}
	}
	for _, pkg := range pkgs {
		visit(pkg)
	}
}

// PrintErrors prints to os.Stderr the accumulated errors of all
// packages in the import graph rooted at pkgs, dependencies first.
// PrintErrors returns the number of errors printed.
func PrintErrors(pkgs []*Package) int {
	var n int
	Visit(pkgs, nil, func(pkg *Package) {
		for _, err := range pkg.Errors {
			fmt.Fprintln(os.Stderr, err)
			n++
		}
	})
	return n
}

//bz: i need the index of pkgs
func VisitAndMore(pkgs []*Package, pre func(*Package) bool, post func(int, *Package)) {
	seen := make(map[*Package]bool)
	var visit func(int, *Package)
	visit = func(idx int, pkg *Package) {
		if !seen[pkg] {
			seen[pkg] = true

			if pre == nil || pre(pkg) {
				paths := make([]string, 0, len(pkg.Imports))
				for path := range pkg.Imports {
					paths = append(paths, path)
				}
				sort.Strings(paths) // Imports is a map, this makes visit stable
				for _, path := range paths {
					visit(-1, pkg.Imports[path])
				}
			}

			if post != nil {
				post(idx, pkg)
			}
		}
	}
	for idx, pkg := range pkgs {
		visit(idx, pkg)
	}
}


//bz: race_checker api -> i want to know who are those pkgs with errors.
//and I also want to exclude those error pkgs from pkgs (input)
func PrintErrorsAndMore(pkgs []*Package) (int, []*Package) {
	var n int
	var errorPkgs []*Package
	VisitAndMore(pkgs, nil, func(idx int, pkg *Package) {
		for _, err := range pkg.Errors {
			fmt.Fprintln(os.Stderr, err)
			//bz: More
			if idx != -1 {
				pkgs[idx] = nil //remove it if it has error
			}
			errorPkgs = append(errorPkgs, pkg) //record
			n++
		}
	})
	return n, errorPkgs
}