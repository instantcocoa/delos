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

This CLI provides commands to manage prompts, datasets, evaluations,
and deployments.

Examples:
  # List prompts
  delos prompt list

  # Create a prompt
  delos prompt create my-prompt --template "Hello {{name}}!"

  # Run an evaluation
  delos eval run --prompt my-prompt --dataset test-cases

  # Deploy a prompt version
  delos deploy create --prompt my-prompt --version 2
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

func init() {
	rootCmd.PersistentFlags().StringVarP(&format, "output", "o", "", "Output format (table, json, yaml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output")

	// Add subcommands
	rootCmd.AddCommand(observeCmd)
	rootCmd.AddCommand(promptCmd)
	rootCmd.AddCommand(runtimeCmd)
	rootCmd.AddCommand(datasetsCmd)
	rootCmd.AddCommand(evalCmd)
	rootCmd.AddCommand(deployCmd)
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
