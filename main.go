package main

import (
	"flag"
	"github.com/o2lab/go2/analyzer"
	"github.com/o2lab/go2/config"
	log "github.com/sirupsen/logrus"
)

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

	analyzer := analyzer.NewAnalyzerConfig(flag.Args(), config.ExcludedPkgs)
	analyzer.Run()
}