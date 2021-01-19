package main_test

import (
	"bytes"
	"fmt"
	"github.com/o2lab/go2/analyzer"
	"github.com/o2lab/go2/config"
	log "github.com/sirupsen/logrus"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"text/scanner"
)

type posKey struct {
	file string
	line int
}

func TestRace(t *testing.T) {
	want := loadTestData(t)
	testOutput := bytes.NewBufferString("")
	results := runTests(testOutput)

	gopath := TestData()
	checkRaceOutput(t, want, results, gopath)
}

func checkRaceOutput(t *testing.T, want map[posKey][]*regexp.Regexp, results map[token.Position][]string, gopath string) {
	checkMessage := func(posn token.Position, message string, gopath string) {
		k := posKey{sanitize(gopath, posn.Filename), posn.Line}
		expects := want[k]
		var unmatched []string
		for i, exp := range expects {
			if exp.MatchString(message) {
				// matched: remove the expectation.
				expects[i] = expects[len(expects)-1]
				expects = expects[:len(expects)-1]
				want[k] = expects
				return
			}
			unmatched = append(unmatched, fmt.Sprintf("%q", exp))
		}
		if unmatched == nil {
			fmt.Printf("%v: unexpected race: %v\n", posn, message)
		} else {
			fmt.Printf("%v: %q does not match pattern %s\n",
				posn, message, strings.Join(unmatched, " or "))
		}
		t.Fail()
	}

	for pos, messages := range results {
		for _, m := range messages {
			checkMessage(pos, m, gopath)
		}
	}

	var surplus []string
	for key, expects := range want {
		for _, exp := range expects {
			err := fmt.Sprintf("%s:%d: no race was reported matching %q", key.file, key.line, exp)
			surplus = append(surplus, err)
		}
	}
	sort.Strings(surplus)
	for _, err := range surplus {
		fmt.Printf("%s\n", err)
		t.Fail()
	}
}

func loadTestData(t *testing.T) map[posKey][]*regexp.Regexp {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, "./tests/testdata", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	want := make(map[posKey][]*regexp.Regexp)

	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, cgroup := range f.Comments {
				for _, c := range cgroup.List {
					if text := strings.TrimPrefix(c.Text, "// want"); text != c.Text {
						text = strings.TrimSpace(text)

						expects, err := parseExpectations(text)
						if err != nil {
							t.Fatal(err)
						}
						if expects != nil {
							pos := fset.Position(c.Pos())
							want[posKey{pos.Filename, pos.Line}] = expects
						}
					}
				}
			}
		}
	}
	return want
}

// parseExpectations parses the content of a "// want ..." comment
// and returns the parsed regular expression for each comment group.
func parseExpectations(text string) ([]*regexp.Regexp, error) {
	var scanErr string
	sc := new(scanner.Scanner).Init(strings.NewReader(text))
	sc.Error = func(s *scanner.Scanner, msg string) {
		scanErr = msg // e.g. bad string escape
	}
	sc.Mode = scanner.ScanStrings | scanner.ScanRawStrings

	scanRegexp := func(tok rune) (*regexp.Regexp, error) {
		if tok != scanner.String && tok != scanner.RawString {
			return nil, fmt.Errorf("got %s, want regular expression",
				scanner.TokenString(tok))
		}
		pattern, _ := strconv.Unquote(sc.TokenText()) // can't fail
		return regexp.Compile(pattern)
	}

	var expects []*regexp.Regexp
	for {
		tok := sc.Scan()
		switch tok {
		case scanner.String, scanner.RawString:
			rx, err := scanRegexp(tok)
			if err != nil {
				return nil, err
			}
			expects = append(expects, rx)

		case scanner.EOF:
			if scanErr != "" {
				return nil, fmt.Errorf("%s", scanErr)
			}
			return expects, nil

		default:
			return nil, fmt.Errorf("unexpected %s", scanner.TokenString(tok))
		}
	}
}

func runTests(writer io.Writer) map[token.Position][]string {
	log.SetLevel(log.InfoLevel)
	log.SetOutput(writer)
	out := make(map[token.Position][]string)
	analyzer := analyzer.NewAnalyzerConfig([]string{"./tests/testdata"}, config.ExcludedPkgs)
	analyzer.SetTestOutput(out)
	analyzer.Run()
	return out
}

// sanitize removes the GOPATH portion of the filename,
// typically a gnarly /tmp directory, and returns the rest.
func sanitize(gopath, filename string) string {
	prefix := gopath + string(os.PathSeparator)
	return filepath.ToSlash(strings.TrimPrefix(filename, prefix))
}

// TestData returns the effective filename of
// the program's "testdata" directory.
// This function may be overridden by projects using
// an alternative build system (such as Blaze) that
// does not run a test in its package directory.
var TestData = func() string {
	testdata, err := filepath.Abs(".")
	if err != nil {
		log.Fatal(err)
	}
	return testdata
}