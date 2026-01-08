package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	evalv1 "github.com/instantcocoa/delos/gen/go/eval/v1"
	"github.com/instantcocoa/delos/cli/internal/output"
)

var evalCmd = &cobra.Command{
	Use:   "eval",
	Short: "Evaluation operations",
	Long:  "Commands for running and managing evaluations.",
}

var evalRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Create and start an evaluation run",
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.EvalAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := evalv1.NewEvalServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		name, _ := cmd.Flags().GetString("name")
		promptID, _ := cmd.Flags().GetString("prompt")
		promptVersion, _ := cmd.Flags().GetInt32("version")
		datasetID, _ := cmd.Flags().GetString("dataset")
		evaluators, _ := cmd.Flags().GetStringSlice("evaluators")

		if name == "" {
			name = fmt.Sprintf("eval-%s", time.Now().Format("20060102-150405"))
		}

		evalConfigs := make([]*evalv1.EvaluatorConfig, len(evaluators))
		for i, e := range evaluators {
			evalConfigs[i] = &evalv1.EvaluatorConfig{
				Type:   e,
				Weight: 1.0,
			}
		}

		resp, err := client.CreateEvalRun(ctx, &evalv1.CreateEvalRunRequest{
			Name:          name,
			PromptId:      promptID,
			PromptVersion: promptVersion,
			DatasetId:     datasetID,
			Config: &evalv1.EvalConfig{
				Evaluators:  evalConfigs,
				Concurrency: 5,
			},
		})
		if err != nil {
			return fmt.Errorf("failed to create eval run: %w", err)
		}

		output.Success("Created evaluation run %s (ID: %s)", resp.EvalRun.Name, resp.EvalRun.Id)
		output.Info("Status: %s", resp.EvalRun.Status.String())

		return nil
	},
}

var evalListCmd = &cobra.Command{
	Use:   "list",
	Short: "List evaluation runs",
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.EvalAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := evalv1.NewEvalServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		promptID, _ := cmd.Flags().GetString("prompt")
		datasetID, _ := cmd.Flags().GetString("dataset")

		resp, err := client.ListEvalRuns(ctx, &evalv1.ListEvalRunsRequest{
			PromptId:  promptID,
			DatasetId: datasetID,
			Limit:     100,
		})
		if err != nil {
			return fmt.Errorf("failed to list eval runs: %w", err)
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			w := output.NewWriter(cfg.Format)
			return w.Print(resp.EvalRuns)
		}

		table := output.Table{
			Headers: []string{"ID", "NAME", "STATUS", "PROGRESS", "SCORE", "CREATED"},
			Rows:    make([][]string, len(resp.EvalRuns)),
		}
		for i, r := range resp.EvalRuns {
			created := ""
			if r.CreatedAt != nil {
				created = r.CreatedAt.AsTime().Format("2006-01-02 15:04")
			}
			progress := fmt.Sprintf("%d/%d", r.CompletedExamples, r.TotalExamples)
			score := "-"
			if r.Summary != nil {
				score = fmt.Sprintf("%.2f", r.Summary.OverallScore)
			}
			table.Rows[i] = []string{
				r.Id[:8],
				r.Name,
				r.Status.String(),
				progress,
				score,
				created,
			}
		}

		w := output.NewWriter("table")
		return w.Print(table)
	},
}

var evalGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Get evaluation run details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.EvalAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := evalv1.NewEvalServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		resp, err := client.GetEvalRun(ctx, &evalv1.GetEvalRunRequest{Id: args[0]})
		if err != nil {
			return fmt.Errorf("failed to get eval run: %w", err)
		}

		w := output.NewWriter(cfg.Format)
		return w.Print(resp.EvalRun)
	},
}

var evalCancelCmd = &cobra.Command{
	Use:   "cancel <id>",
	Short: "Cancel an evaluation run",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.EvalAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := evalv1.NewEvalServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		_, err = client.CancelEvalRun(ctx, &evalv1.CancelEvalRunRequest{Id: args[0]})
		if err != nil {
			return fmt.Errorf("failed to cancel eval run: %w", err)
		}

		output.Success("Cancelled evaluation run %s", args[0])
		return nil
	},
}

var evalResultsCmd = &cobra.Command{
	Use:   "results <run-id>",
	Short: "Get evaluation results",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.EvalAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := evalv1.NewEvalServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		failedOnly, _ := cmd.Flags().GetBool("failed")
		limit, _ := cmd.Flags().GetInt32("limit")

		resp, err := client.GetEvalResults(ctx, &evalv1.GetEvalResultsRequest{
			EvalRunId:  args[0],
			FailedOnly: failedOnly,
			Limit:      limit,
		})
		if err != nil {
			return fmt.Errorf("failed to get results: %w", err)
		}

		output.Info("Found %d results (showing %d)", resp.TotalCount, len(resp.Results))

		w := output.NewWriter(cfg.Format)
		return w.Print(resp.Results)
	},
}

var evalCompareCmd = &cobra.Command{
	Use:   "compare <run-a> <run-b>",
	Short: "Compare two evaluation runs",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.EvalAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := evalv1.NewEvalServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		resp, err := client.CompareRuns(ctx, &evalv1.CompareRunsRequest{
			RunIdA: args[0],
			RunIdB: args[1],
		})
		if err != nil {
			return fmt.Errorf("failed to compare runs: %w", err)
		}

		w := output.NewWriter(cfg.Format)
		return w.Print(resp)
	},
}

var evalEvaluatorsCmd = &cobra.Command{
	Use:   "evaluators",
	Short: "List available evaluators",
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.EvalAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := evalv1.NewEvalServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		resp, err := client.ListEvaluators(ctx, &evalv1.ListEvaluatorsRequest{})
		if err != nil {
			return fmt.Errorf("failed to list evaluators: %w", err)
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			w := output.NewWriter(cfg.Format)
			return w.Print(resp.Evaluators)
		}

		table := output.Table{
			Headers: []string{"TYPE", "NAME", "DESCRIPTION"},
			Rows:    make([][]string, len(resp.Evaluators)),
		}
		for i, e := range resp.Evaluators {
			desc := e.Description
			if len(desc) > 50 {
				desc = desc[:47] + "..."
			}
			table.Rows[i] = []string{e.Type, e.Name, desc}
		}

		w := output.NewWriter("table")
		return w.Print(table)
	},
}

func init() {
	// Run flags
	evalRunCmd.Flags().String("name", "", "Run name")
	evalRunCmd.Flags().String("prompt", "", "Prompt ID")
	evalRunCmd.Flags().Int32("version", 0, "Prompt version (0 = latest)")
	evalRunCmd.Flags().String("dataset", "", "Dataset ID")
	evalRunCmd.Flags().StringSlice("evaluators", []string{"exact_match"}, "Evaluator types")

	// List flags
	evalListCmd.Flags().String("prompt", "", "Filter by prompt ID")
	evalListCmd.Flags().String("dataset", "", "Filter by dataset ID")

	// Results flags
	evalResultsCmd.Flags().Bool("failed", false, "Only show failed results")
	evalResultsCmd.Flags().Int32("limit", 20, "Max results to show")

	evalCmd.AddCommand(evalRunCmd)
	evalCmd.AddCommand(evalListCmd)
	evalCmd.AddCommand(evalGetCmd)
	evalCmd.AddCommand(evalCancelCmd)
	evalCmd.AddCommand(evalResultsCmd)
	evalCmd.AddCommand(evalCompareCmd)
	evalCmd.AddCommand(evalEvaluatorsCmd)
}
