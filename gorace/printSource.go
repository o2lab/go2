package main

import (
	"bufio"
	"os"
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