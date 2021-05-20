package main

import (
	"bufio"
	log "github.com/sirupsen/logrus"
	"go/token"
	"os"
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
//
//func findVar(pos token.Position) string {
//	theLine, _ := getLineNumber(pos.Filename, pos.Line)
//	tabs := len(theLine)-len(strings.TrimLeftFunc(theLine, unicode.IsSpace))
//	idxStart := pos.Column - tabs
//	idxEnd := idxStart
//	for i:= idxStart; i < len(theLine); i++ {
//		c := theLine[i]
//		s := string(c)
//		if unicode.IsLetter(s) || unicode.IsNumber(s) {
//
//		}
//	}
//	return theLine[idxStart:idxEnd]
//}

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

