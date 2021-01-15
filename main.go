package main

import (
	"flag"
	analyzer "github.com/o2lab/go2/analyzer"
	log "github.com/sirupsen/logrus"
)

var excluded = []string{
	//"sync",
	//"fmt",
	//"log",

	"context",
	"runtime",
	"internal",
	"race",
	"unsafe",
	"debug",
	"os",
	"crypto",
	"regexp",
	"strconv",
	"bytes",
	"math",
	"unicode",
	"encoding",
	"time",
	"reflect",
	"sort",
}


func main() {
	debug := flag.Bool("debug", false, "Prints debug messages.")
	help := flag.Bool("help", false, "Show all command-line options.")
	flag.Parse()
	if *help {
		log.Println("Usage:")
		flag.PrintDefaults()
		return
	}
	if *debug {
		log.SetLevel(log.DebugLevel)
	}
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "15:04:05",
	})
	config := analyzer.NewAnalyzerConfig(flag.Args(), excluded)
	config.Run()
}