package gorace

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

func printSource(rwPos token.Position) {
	for lineNum := rwPos.Line-3; lineNum <= rwPos.Line+3; lineNum++ {
		theLine, _ := getLineNumber(rwPos.Filename, lineNum)
		if lineNum == rwPos.Line {
			tabs := len(theLine)-len(strings.TrimLeftFunc(theLine, unicode.IsSpace))
			spaces := rwPos.Column-tabs
			if spaces < 1 {
				spaces = 1
			}
			log.Info("\t>> ", strings.Repeat("\t", tabs), strings.Repeat(" ", spaces-1), "v")
			log.Info("\t>> ", theLine)
			log.Info("\t>> ", strings.Repeat("\t", tabs), strings.Repeat(" ", spaces-1), "^")
		} else {
			log.Info("\t>> ", theLine)
		}
	}
}

