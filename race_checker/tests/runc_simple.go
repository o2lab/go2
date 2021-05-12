package main

import (
	"fmt"
	"github.com/urfave/cli"
)

var x int

var checkpointCommand = cli.Command{
	Name:  "checkpoint",
	Usage: "checkpoint a running container",
	ArgsUsage: `<container-id>

Where "<container-id>" is the name for the instance of the container to be
checkpointed.`,
	Description: `The checkpoint command saves the state of the container instance.`,
	Flags: []cli.Flag{
		cli.StringFlag{Name: "image-path", Value: "", Usage: "path for saving criu image files"},
	},
	Action: someFn(),
}

func someFn() int {
	x /* RACE Write */ = 3
	fmt.Println(x)
	return x
}

func main() {
	app := cli.NewApp()
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "debug",
			Usage: "enable debug output for logging",
		},
		cli.StringFlag{
			Name:  "log",
			Value: "",
			Usage: "set the log file path where internal debug information is written",
		},
	}
	go func() {
		x /* RACE Write */  = 2
	}()
	app.Commands = []cli.Command{
		checkpointCommand,
	}
	fmt.Println(app.Commands[0].Action)
}