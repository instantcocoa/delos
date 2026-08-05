// Command delos is the Delos control plane and CLI in one binary:
// `delos serve` runs the control plane; every other subcommand is a client.
package main

import (
	"os"

	"github.com/instantcocoa/delos/cli/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
