package cmd

import (
	"github.com/spf13/cobra"

	"github.com/instantcocoa/delos/controlplane"
)

// serveCmd runs the Delos control plane in the foreground.
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the Delos control plane server",
	Long: `Run the Delos control plane: observe, prompt, datasets, eval, and
deploy in one process behind one port (default 8081).

Configuration comes from DELOS_* environment variables; see the deployment
docs. The LLM data plane is the separate delos-gateway binary.`,
	// A failed server start is an operational error, not a usage error.
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return controlplane.Run()
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}
