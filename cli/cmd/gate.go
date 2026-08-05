package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/instantcocoa/delos/cli/internal/output"
	deployv1 "github.com/instantcocoa/delos/gen/go/deploy/v1"
)

// gateCmd manages quality gates: named threshold sets evaluated against the
// latest completed eval run for a prompt. CI systems call `delos gate check`.
var gateCmd = &cobra.Command{
	Use:     "gate",
	Aliases: []string{"deploy"}, // the old command name keeps working
	Short:   "Manage quality gates",
	Long: `Quality gates turn eval results into CI verdicts.

A gate names a prompt and a set of conditions (e.g. overall_score>=0.8).
"delos gate check <name>" evaluates the gate against the latest completed
eval run and exits 0 (pass) or 1 (fail), so CI pipelines can block on it.`,
}

func getGateClient() (deployv1.DeployServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(cfg.ControlPlaneAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to control plane at %s: %w", cfg.ControlPlaneAddr, err)
	}
	return deployv1.NewDeployServiceClient(conn), conn, nil
}

var gateCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a quality gate",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		promptID, _ := cmd.Flags().GetString("prompt")
		description, _ := cmd.Flags().GetString("description")
		conditions, _ := cmd.Flags().GetStringArray("condition")

		if promptID == "" {
			return fmt.Errorf("--prompt is required")
		}
		if len(conditions) == 0 {
			return fmt.Errorf("at least one --condition is required (e.g. --condition \"overall_score>=0.8\")")
		}

		req := &deployv1.CreateQualityGateRequest{
			Name:        args[0],
			Description: description,
			PromptId:    promptID,
		}
		for _, c := range conditions {
			cond, err := parseConditionFlag(c)
			if err != nil {
				return err
			}
			req.Conditions = append(req.Conditions, cond)
		}

		client, conn, err := getGateClient()
		if err != nil {
			return err
		}
		defer conn.Close()

		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()
		resp, err := client.CreateQualityGate(ctx, req)
		if err != nil {
			return fmt.Errorf("failed to create gate: %w", err)
		}
		output.Success(fmt.Sprintf("Created gate %q for prompt %s with %d condition(s)",
			resp.QualityGate.Name, resp.QualityGate.PromptId, len(resp.QualityGate.Conditions)))
		return nil
	},
}

// parseConditionFlag parses "metric>=value" / "metric<=value".
func parseConditionFlag(s string) (*deployv1.GateCondition, error) {
	for _, op := range []struct {
		token string
		enum  deployv1.GateOperator
	}{
		{">=", deployv1.GateOperator_GATE_OPERATOR_GTE},
		{"<=", deployv1.GateOperator_GATE_OPERATOR_LTE},
	} {
		if metric, rest, ok := cutString(s, op.token); ok {
			var threshold float64
			if _, err := fmt.Sscanf(rest, "%f", &threshold); err != nil {
				return nil, fmt.Errorf("condition %q: cannot parse threshold %q", s, rest)
			}
			return &deployv1.GateCondition{
				Metric:    trimSpace(metric),
				Operator:  op.enum,
				Threshold: threshold,
			}, nil
		}
	}
	return nil, fmt.Errorf("condition %q must use >= or <= (e.g. \"overall_score>=0.8\")", s)
}

var gateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List quality gates",
	RunE: func(cmd *cobra.Command, args []string) error {
		promptID, _ := cmd.Flags().GetString("prompt")

		client, conn, err := getGateClient()
		if err != nil {
			return err
		}
		defer conn.Close()

		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()
		resp, err := client.ListQualityGates(ctx, &deployv1.ListQualityGatesRequest{PromptId: promptID})
		if err != nil {
			return fmt.Errorf("failed to list gates: %w", err)
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			return output.NewWriter(cfg.Format).Print(resp.QualityGates)
		}
		table := output.Table{Headers: []string{"NAME", "PROMPT", "CONDITIONS", "DESCRIPTION"}}
		for _, g := range resp.QualityGates {
			conds := ""
			for i, c := range g.Conditions {
				if i > 0 {
					conds += ", "
				}
				conds += fmt.Sprintf("%s%s%g", c.Metric, operatorToken(c.Operator), c.Threshold)
			}
			table.Rows = append(table.Rows, []string{g.Name, g.PromptId, conds, g.Description})
		}
		return output.NewWriter("table").Print(table)
	},
}

func operatorToken(op deployv1.GateOperator) string {
	switch op {
	case deployv1.GateOperator_GATE_OPERATOR_GTE:
		return ">="
	case deployv1.GateOperator_GATE_OPERATOR_LTE:
		return "<="
	default:
		return "?"
	}
}

var gateCheckCmd = &cobra.Command{
	Use:   "check <name>",
	Short: "Evaluate a gate; exit 0 on pass, 1 on fail (for CI)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, conn, err := getGateClient()
		if err != nil {
			return err
		}
		defer conn.Close()

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()
		resp, err := client.GetGateVerdict(ctx, &deployv1.GetGateVerdictRequest{Name: args[0]})
		if err != nil {
			return fmt.Errorf("failed to get verdict: %w", err)
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			if err := output.NewWriter(cfg.Format).Print(resp); err != nil {
				return err
			}
		} else {
			state := "PASS"
			if !resp.Pass {
				state = "FAIL"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Gate %q: %s\n", args[0], state)
			for _, reason := range resp.Reasons {
				fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", reason)
			}
		}

		if !resp.Pass {
			// CI contract: non-zero exit on failure, without cobra usage noise.
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			os.Exit(1)
		}
		return nil
	},
}

// small local helpers to avoid extra imports
func cutString(s, sep string) (before, after string, found bool) {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return s[:i], s[i+len(sep):], true
		}
	}
	return s, "", false
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

func init() {
	gateCreateCmd.Flags().String("prompt", "", "Prompt ID or slug the gate evaluates (required)")
	gateCreateCmd.Flags().String("description", "", "Gate description")
	gateCreateCmd.Flags().StringArray("condition", nil, `Condition, repeatable (e.g. "overall_score>=0.8")`)
	gateListCmd.Flags().String("prompt", "", "Filter by prompt")

	gateCmd.AddCommand(gateCreateCmd)
	gateCmd.AddCommand(gateListCmd)
	gateCmd.AddCommand(gateCheckCmd)
}
