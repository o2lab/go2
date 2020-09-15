package main

import (
	"flag"
	"fmt"
	"github.com/rogpeppe/go-internal/testenv"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"io/ioutil"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	haltOnError = flag.Bool("halt", false, "halt on error")
	listErrors  = flag.Bool("errlist", false, "list errors")
	testFiles   = flag.String("files", "", "space-separated list of test files")
)

var tests = []string{
	"tests/cg.go",
	"tests/test1.go",
	"tests/waitgroup.go",
}

var fset = token.NewFileSet()

// Positioned errors are of the form "(Read|Write) of ... at filename:line:column".
var posMsgRx = regexp.MustCompile(`^ (.*) at (.*:[0-9]+:[0-9]+)$`)

// splitError splits an error's error message into a position string
// and the actual error message. If there's no position information,
// pos is the empty string, and msg is the entire error message.
//
func splitError(err error) (pos, msg string) {
	msg = err.Error()
	if m := posMsgRx.FindStringSubmatch(msg); len(m) == 3 {
		msg = m[1]
		pos = m[2]
	}
	return
}

// RACE comments must start with text `RACE "rx"` or `RACE rx` where
// rx is a regular expression that matches the expected error message.
// Space around "rx" or rx is ignored. Use the form `RACE HERE "rx"`
// for error messages that are located immediately after rather than
// at a token's position.
//
var errRx = regexp.MustCompile(`^ *RACE *(HERE)? *"?([^"]*)"?`)

// errMap collects the regular expressions of ERROR comments found
// in files and returns them as a map of error positions to error messages.
//
func errMap(t *testing.T, testname string, files []*ast.File) map[string][]string {
	// map of position strings to lists of error message patterns
	errmap := make(map[string][]string)

	for _, file := range files {
		filename := fset.Position(file.Package).Filename
		src, err := ioutil.ReadFile(filename)
		if err != nil {
			t.Fatalf("%s: could not read %s", testname, filename)
		}
		filename, _ = filepath.Abs(filename)

		var s scanner.Scanner
		s.Init(fset.AddFile(filename, -1, len(src)), src, nil, scanner.ScanComments)
		var prev token.Pos // position of last non-comment, non-semicolon token
		var here token.Pos // position immediately after the token at position prev

	scanFile:
		for {
			pos, tok, lit := s.Scan()
			switch tok {
			case token.EOF:
				break scanFile
			case token.COMMENT:
				if lit[1] == '*' {
					lit = lit[:len(lit)-2] // strip trailing */
				}
				if s := errRx.FindStringSubmatch(lit[2:]); len(s) == 3 {
					pos := prev
					if s[1] == "HERE" {
						pos = here
					}
					p := fset.Position(pos).String()
					errmap[p] = append(errmap[p], strings.TrimSpace(s[2]))
				}
			case token.SEMICOLON:
				// ignore automatically inserted semicolon
				if lit == "\n" {
					continue scanFile
				}
				fallthrough
			default:
				prev = pos
				var l int // token length
				if tok.IsLiteral() {
					l = len(lit)
				} else {
					l = len(tok.String())
				}
				here = prev + token.Pos(l)
			}
		}
	}

	return errmap
}

func eliminate(t *testing.T, errmap map[string][]string, racyStackTops []error) {
	for _, err := range racyStackTops {
		pos, gotMsg := splitError(err)
		list := errmap[pos]
		index := -1 // list index of matching message, if any
		// we expect one of the messages in list to match the error at pos
		for i, wantRx := range list {
			rx, err := regexp.Compile(wantRx)
			if err != nil {
				t.Errorf("%s: %v", pos, err)
				continue
			}
			if rx.MatchString(gotMsg) {
				index = i
				break
			}
		}
		if index >= 0 {
			// eliminate from list
			if n := len(list) - 1; n > 0 {
				// not the last entry - swap in last element and shorten list by 1
				list[index] = list[n]
				errmap[pos] = list[:n]
			} else {
				// last entry - remove list from map
				delete(errmap, pos)
			}
		} else {
			t.Errorf("%s: no race expected: %q", pos, gotMsg)
		}
	}
}

func runChecker(t *testing.T, filenames []string) ([]*ast.File, []error) {
	var files []*ast.File
	for _, filename := range filenames {
		file, err := parser.ParseFile(fset, filename, nil, parser.AllErrors)
		if file == nil {
			t.Fatalf("%s: %s", filename, err)
		}
		files = append(files, file)
	}
	racyStackTops = []string{}
	err := staticAnalysis(filenames)
	if err != nil {
		t.Fatal(err)
	}
	var raceErrors []error
	for _, msg := range racyStackTops {
		raceErrors = append(raceErrors, fmt.Errorf(msg))
	}

	return files, raceErrors
}

func checkFile(t *testing.T, testfiles []string) {
	// parse files and collect parser errors
	files, errlist := runChecker(t, testfiles)

	if *listErrors && len(errlist) > 0 {
		t.Errorf("--- %s:", testfiles)
		for _, err := range errlist {
			t.Error(err)
		}
	}

	// typecheck and collect typechecker errors
	var conf types.Config
	conf.Importer = importer.Default()
	conf.Error = func(err error) {
		if *haltOnError {
			defer panic(err)
		}
		if *listErrors {
			t.Error(err)
			return
		}
	}

	if *listErrors {
		return
	}

	pkgName := "main"
	// match and eliminate errors;
	// we are expecting the following errors
	errmap := errMap(t, pkgName, files)
	eliminate(t, errmap, errlist)

	// there should be no expected errors left
	if len(errmap) > 0 {
		t.Errorf("--- %s: %d source positions with expected (but not reported) races:", pkgName, len(errmap))
		for pos, list := range errmap {
			for _, rx := range list {
				t.Errorf("%s: %q", pos, rx)
			}
		}
	}
}

func TestRace(t *testing.T) {
	testenv.MustHaveGoBuild(t)

	// If explicit test files are specified, only check those.
	if files := *testFiles; files != "" {
		checkFile(t, strings.Split(files, " "))
		return
	}

	// Otherwise, run all the tests.
	for _, file := range tests {
		checkFile(t, []string{file})
	}
}
