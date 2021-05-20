package main

import (
	"bufio"
	log "github.com/sirupsen/logrus"
	"go/token"
	"os"
	"regexp"
	"strings"
	"unicode"
)

func getLineNumber(filePath string, lineNum int) (string, error) {
	sourceFile, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	scanner := bufio.NewScanner(sourceFile)
	lineStr := ""
	for i := 1; scanner.Scan(); i++ {
		if i == lineNum {
			lineStr = scanner.Text()
			break
		}
	}
	err = sourceFile.Close()
	if err != nil {
		return "", err
	}
	return lineStr, err
}

func printVarName(rwPos token.Position) string {
	lineNum := rwPos.Line
	theLine, _ := getLineNumber(rwPos.Filename, lineNum)
	lineRmTabs := strings.TrimLeftFunc(theLine, unicode.IsSpace)
	tabs := len(theLine)-len(lineRmTabs)
	spaces := rwPos.Column-tabs // should be minimum 0
	var isStringAlphabetic = regexp.MustCompile(`^[a-zA-Z0-9_]`).MatchString
	for i := spaces; i < len(lineRmTabs); i++ {
		if !isStringAlphabetic(lineRmTabs[i:i+1]) {
			return lineRmTabs[spaces-1:i] // TODO: test result to make sure it looks correct
		}
	}
	return lineRmTabs[spaces-1:spaces]
}

func printSource(rwPos token.Position) {
	for lineNum := rwPos.Line-3; lineNum <= rwPos.Line+3; lineNum++ {
		theLine, _ := getLineNumber(rwPos.Filename, lineNum)
		if lineNum == rwPos.Line {
			tabs := len(theLine)-len(strings.TrimLeftFunc(theLine, unicode.IsSpace))
			spaces := rwPos.Column-tabs
			log.Info("\t>> ", strings.Repeat("\t", tabs), strings.Repeat(" ", spaces-1), "v")
			log.Info("\t>> ", theLine)
			log.Info("\t>> ", strings.Repeat("\t", tabs), strings.Repeat(" ", spaces-1), "^")
		} else {
			log.Info("\t>> ", theLine)
		}
	}
}

