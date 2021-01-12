package main

import (
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
	Action: func(context *cli.Context) error {

		//container, err := getContainer(context)
		//if err != nil {
		//	return err
		//}
		//
		//return container.Checkpoint(options)
		return someFn()
	},
}

func someFn() error {
	x /* RACE Write */ = 3
	return nil
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
	go func() { x /* RACE Write */  = 2 }()
	app.Commands = []cli.Command{
		checkpointCommand,
	}

}