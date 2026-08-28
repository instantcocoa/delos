// Package cmd contains CLI commands.
package cmd

import (
	"github.com/spf13/cobra"

	"github.com/instantcocoa/delos/cli/internal/config"
)

var (
	cfg     *config.Config
	format  string
	verbose bool
)

// rootCmd is the base command.
var rootCmd = &cobra.Command{
	Use:   "delos",
	Short: "Delos CLI - LLM Infrastructure Platform",
	Long: `Delos is a unified infrastructure platform for LLM applications.

This CLI manages prompts, datasets, evaluations and quality gates on the
control plane (DELOS_CONTROL_PLANE_ADDR, default localhost:8081), and drives
the gateway's HTTP API (DELOS_GATEWAY_URL, default http://localhost:8080).

Command aliases: "deploy" is an alias for "gate"; "runtime" is an alias for
"gateway"; "dataset"/"ds" are aliases for "datasets".

Examples:
  # Create a prompt (a slug is derived from the name unless --slug is given)
  delos prompt create summarizer --system "You summarize text." --user "Summarize: {{input}}"

  # Inspect it by slug, and look at an older version
  delos prompt get summarizer
  delos prompt get summarizer --reference summarizer:v1
  delos prompt history summarizer
  delos prompt compare summarizer 1 2

  # Build a dataset to evaluate against
  delos datasets create smoke-tests
  delos datasets add <dataset-id> --example 'hello=>Hello!' --example 'bye=>Goodbye!'
  delos datasets examples <dataset-id>
  delos datasets export <dataset-id> --format jsonl --file smoke-tests.jsonl

  # Run an evaluation (--prompt accepts an ID or a slug)
  delos eval run --prompt summarizer --dataset <dataset-id> --model claude-haiku-4-5
  delos eval list

  # Gate CI on the latest eval run for a prompt
  delos gate create nightly --prompt summarizer --condition "overall_score>=0.8"
  delos gate check nightly

  # Talk to the gateway directly
  delos gateway models
`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		cfg = config.DefaultConfig()
		if format != "" {
			cfg.Format = format
		}
		cfg.Verbose = verbose
	},
}

// Execute runs the CLI.
func Execute() error {
	return rootCmd.Execute()
}

// shortID truncates an identifier for table display without panicking on IDs
// shorter than n (slicing an arbitrary-length ID is not safe).
func shortID(id string, n int) string {
	if n <= 0 || len(id) <= n {
		return id
	}
	return id[:n]
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&format, "output", "o", "", "Output format (table, json, yaml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output")

	// Add subcommands
	rootCmd.AddCommand(observeCmd)
	rootCmd.AddCommand(promptCmd)
	rootCmd.AddCommand(gatewayCmd)
	rootCmd.AddCommand(datasetsCmd)
	rootCmd.AddCommand(evalCmd)
	rootCmd.AddCommand(gateCmd)
	rootCmd.AddCommand(versionCmd)
}

// versionCmd prints version info.
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Println("delos version 0.1.0")
	},
}
