package util

import (
	"bufio"
	"os"
	"strings"
)

/*
bz: the following are common util between gorace and gopta -> avoid circular import
 */

//bz: tmpScope -> pkg; dir -> target path
func GetScopeFromGOModRecursive(tmpScope string, dir string) string {
	checkPath := dir
	idx := len(checkPath)

	for idx > 0 {
		checkPath := checkPath[:idx]
		modScope := GetScopeFromGOMod(checkPath)
		if modScope != "" {
			if tmpScope != "" {
				if strings.HasPrefix(tmpScope, modScope) {
					return modScope
				}
			} else { //TODO: bz: how do we determine this is the corresponding go.mod
				return modScope
			}
		} else {
			idx = strings.LastIndex(checkPath, "/")
		}
	}
	return ""
}

//bz:
func GetScopeFromGOMod(path string) string {
	if path == "" {
		var err error
		path, err = os.Getwd() //current working directory == project path
		if err != nil {
			panic("Error while os.Getwd: " + err.Error())
		}
	}

	gomodFile, err := os.Open(path + "/go.mod") // For read access.
	if err != nil {
		return "" //no such file
	}
	defer gomodFile.Close()
	scanner := bufio.NewScanner(gomodFile)
	var mod string
	for scanner.Scan() {
		s := scanner.Text()
		if strings.HasPrefix(s, "module ") {
			mod = s
			break //this is the line "module xxx.xxx.xx/xxx"
		}
	}
	if mod == "" {
		return "" //not format as expected
	}
	if err2 := scanner.Err(); err2 != nil {
		return "" //scan error
	}
	parts := strings.Split(mod, " ")
	return parts[1]
}
