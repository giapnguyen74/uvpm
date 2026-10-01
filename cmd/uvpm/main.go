package main

import (
	"os"

	"github.com/giapnguyen74/uvpm/internal/cli"
)

func main() { os.Exit(cli.Run(os.Args[1:])) }
