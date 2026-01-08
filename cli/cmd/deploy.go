package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	deployv1 "github.com/instantcocoa/delos/gen/go/deploy/v1"
	"github.com/instantcocoa/delos/cli/internal/output"
)

var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Deployment operations",
	Long:  "Commands for managing deployments and rollouts.",
}

var deployCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new deployment",
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.DeployAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := deployv1.NewDeployServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		promptID, _ := cmd.Flags().GetString("prompt")
		toVersion, _ := cmd.Flags().GetInt32("version")
		environment, _ := cmd.Flags().GetString("environment")
		deployType, _ := cmd.Flags().GetString("type")
		initialPct, _ := cmd.Flags().GetInt32("initial-traffic")
		skipApproval, _ := cmd.Flags().GetBool("skip-approval")
		autoRollback, _ := cmd.Flags().GetBool("auto-rollback")

		var dt deployv1.DeploymentType
		switch deployType {
		case "immediate":
			dt = deployv1.DeploymentType_DEPLOYMENT_TYPE_IMMEDIATE
		case "gradual":
			dt = deployv1.DeploymentType_DEPLOYMENT_TYPE_GRADUAL
		case "canary":
			dt = deployv1.DeploymentType_DEPLOYMENT_TYPE_CANARY
		case "blue_green", "blue-green":
			dt = deployv1.DeploymentType_DEPLOYMENT_TYPE_BLUE_GREEN
		default:
			dt = deployv1.DeploymentType_DEPLOYMENT_TYPE_IMMEDIATE
		}

		resp, err := client.CreateDeployment(ctx, &deployv1.CreateDeploymentRequest{
			PromptId:     promptID,
			ToVersion:    toVersion,
			Environment:  environment,
			SkipApproval: skipApproval,
			Strategy: &deployv1.DeploymentStrategy{
				Type:              dt,
				InitialPercentage: initialPct,
				AutoRollback:      autoRollback,
			},
		})
		if err != nil {
			return fmt.Errorf("failed to create deployment: %w", err)
		}

		output.Success("Created deployment %s", resp.Deployment.Id)
		output.Info("Status: %s", resp.Deployment.Status.String())
		output.Info("Environment: %s", resp.Deployment.Environment)

		return nil
	},
}

var deployListCmd = &cobra.Command{
	Use:   "list",
	Short: "List deployments",
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.DeployAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := deployv1.NewDeployServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		promptID, _ := cmd.Flags().GetString("prompt")
		environment, _ := cmd.Flags().GetString("environment")

		resp, err := client.ListDeployments(ctx, &deployv1.ListDeploymentsRequest{
			PromptId:    promptID,
			Environment: environment,
			Limit:       100,
		})
		if err != nil {
			return fmt.Errorf("failed to list deployments: %w", err)
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			w := output.NewWriter(cfg.Format)
			return w.Print(resp.Deployments)
		}

		table := output.Table{
			Headers: []string{"ID", "PROMPT", "FROM", "TO", "ENV", "TYPE", "STATUS"},
			Rows:    make([][]string, len(resp.Deployments)),
		}
		for i, d := range resp.Deployments {
			promptID := d.PromptId
			if len(promptID) > 8 {
				promptID = promptID[:8]
			}
			deployType := "immediate"
			if d.Strategy != nil {
				deployType = formatDeployType(d.Strategy.Type)
			}
			table.Rows[i] = []string{
				d.Id[:8],
				promptID,
				fmt.Sprintf("v%d", d.FromVersion),
				fmt.Sprintf("v%d", d.ToVersion),
				d.Environment,
				deployType,
				d.Status.String(),
			}
		}

		w := output.NewWriter("table")
		return w.Print(table)
	},
}

var deployGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Get deployment details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.DeployAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := deployv1.NewDeployServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		resp, err := client.GetDeployment(ctx, &deployv1.GetDeploymentRequest{Id: args[0]})
		if err != nil {
			return fmt.Errorf("failed to get deployment: %w", err)
		}

		w := output.NewWriter(cfg.Format)
		return w.Print(resp.Deployment)
	},
}

var deployApproveCmd = &cobra.Command{
	Use:   "approve <id>",
	Short: "Approve a deployment",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.DeployAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := deployv1.NewDeployServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		comment, _ := cmd.Flags().GetString("comment")

		_, err = client.ApproveDeployment(ctx, &deployv1.ApproveDeploymentRequest{
			Id:      args[0],
			Comment: comment,
		})
		if err != nil {
			return fmt.Errorf("failed to approve deployment: %w", err)
		}

		output.Success("Approved deployment %s", args[0])
		return nil
	},
}

var deployRollbackCmd = &cobra.Command{
	Use:   "rollback <id>",
	Short: "Rollback a deployment",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.DeployAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := deployv1.NewDeployServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		reason, _ := cmd.Flags().GetString("reason")

		resp, err := client.RollbackDeployment(ctx, &deployv1.RollbackDeploymentRequest{
			Id:     args[0],
			Reason: reason,
		})
		if err != nil {
			return fmt.Errorf("failed to rollback deployment: %w", err)
		}

		output.Success("Initiated rollback for deployment %s", args[0])
		output.Info("New status: %s", resp.Deployment.Status.String())
		if resp.RollbackDeployment != nil {
			output.Info("Rollback deployment: %s", resp.RollbackDeployment.Id)
		}
		return nil
	},
}

var deployCancelCmd = &cobra.Command{
	Use:   "cancel <id>",
	Short: "Cancel a deployment",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.DeployAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := deployv1.NewDeployServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		reason, _ := cmd.Flags().GetString("reason")

		_, err = client.CancelDeployment(ctx, &deployv1.CancelDeploymentRequest{
			Id:     args[0],
			Reason: reason,
		})
		if err != nil {
			return fmt.Errorf("failed to cancel deployment: %w", err)
		}

		output.Success("Cancelled deployment %s", args[0])
		return nil
	},
}

var deployStatusCmd = &cobra.Command{
	Use:   "status <id>",
	Short: "Get deployment status",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.DeployAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := deployv1.NewDeployServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		resp, err := client.GetDeploymentStatus(ctx, &deployv1.GetDeploymentStatusRequest{Id: args[0]})
		if err != nil {
			return fmt.Errorf("failed to get status: %w", err)
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			w := output.NewWriter(cfg.Format)
			return w.Print(resp)
		}

		output.Info("Deployment: %s", args[0])
		output.Info("Status: %s", resp.Status.String())

		if resp.Rollout != nil {
			output.Info("\nRollout Progress:")
			output.Info("  Current: %d%%", resp.Rollout.CurrentPercentage)
			output.Info("  Target: %d%%", resp.Rollout.TargetPercentage)
		}

		if resp.CurrentMetrics != nil {
			output.Info("\nCurrent Metrics:")
			output.Info("  Requests: %d", resp.CurrentMetrics.RequestCount)
			output.Info("  Error Rate: %.2f%%", resp.CurrentMetrics.ErrorRate*100)
			output.Info("  Avg Latency: %.0fms", resp.CurrentMetrics.AvgLatencyMs)
			output.Info("  Quality Score: %.2f", resp.CurrentMetrics.QualityScore)
		}

		if len(resp.GateResults) > 0 {
			output.Info("\nQuality Gates:")
			for _, g := range resp.GateResults {
				status := "passed"
				if !g.Passed {
					status = "failed"
				}
				output.Info("  %s: %s", g.GateName, status)
			}
		}

		return nil
	},
}

var deployGatesCmd = &cobra.Command{
	Use:   "gates <prompt-id>",
	Short: "List quality gates for a prompt",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.DeployAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := deployv1.NewDeployServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		resp, err := client.ListQualityGates(ctx, &deployv1.ListQualityGatesRequest{PromptId: args[0]})
		if err != nil {
			return fmt.Errorf("failed to list quality gates: %w", err)
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			w := output.NewWriter(cfg.Format)
			return w.Print(resp.QualityGates)
		}

		table := output.Table{
			Headers: []string{"ID", "NAME", "CONDITIONS", "REQUIRED"},
			Rows:    make([][]string, len(resp.QualityGates)),
		}
		for i, g := range resp.QualityGates {
			required := "no"
			if g.Required {
				required = "yes"
			}
			table.Rows[i] = []string{
				g.Id[:8],
				g.Name,
				fmt.Sprintf("%d", len(g.Conditions)),
				required,
			}
		}

		w := output.NewWriter("table")
		return w.Print(table)
	},
}

var deployGateCreateCmd = &cobra.Command{
	Use:   "gate-create",
	Short: "Create a quality gate",
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.DeployAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := deployv1.NewDeployServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		name, _ := cmd.Flags().GetString("name")
		promptID, _ := cmd.Flags().GetString("prompt")
		required, _ := cmd.Flags().GetBool("required")
		condType, _ := cmd.Flags().GetString("condition-type")
		operator, _ := cmd.Flags().GetString("operator")
		threshold, _ := cmd.Flags().GetFloat64("threshold")

		resp, err := client.CreateQualityGate(ctx, &deployv1.CreateQualityGateRequest{
			Name:     name,
			PromptId: promptID,
			Required: required,
			Conditions: []*deployv1.GateCondition{
				{
					Type:      condType,
					Operator:  operator,
					Threshold: threshold,
				},
			},
		})
		if err != nil {
			return fmt.Errorf("failed to create quality gate: %w", err)
		}

		output.Success("Created quality gate %s (ID: %s)", resp.QualityGate.Name, resp.QualityGate.Id)
		return nil
	},
}

func formatDeployType(dt deployv1.DeploymentType) string {
	switch dt {
	case deployv1.DeploymentType_DEPLOYMENT_TYPE_IMMEDIATE:
		return "immediate"
	case deployv1.DeploymentType_DEPLOYMENT_TYPE_GRADUAL:
		return "gradual"
	case deployv1.DeploymentType_DEPLOYMENT_TYPE_CANARY:
		return "canary"
	case deployv1.DeploymentType_DEPLOYMENT_TYPE_BLUE_GREEN:
		return "blue-green"
	default:
		return "unknown"
	}
}

func init() {
	// Create flags
	deployCreateCmd.Flags().String("prompt", "", "Prompt ID")
	deployCreateCmd.Flags().Int32("version", 0, "Prompt version to deploy")
	deployCreateCmd.Flags().String("environment", "staging", "Target environment")
	deployCreateCmd.Flags().String("type", "immediate", "Deployment type (immediate, gradual, canary, blue-green)")
	deployCreateCmd.Flags().Int32("initial-traffic", 100, "Initial traffic percentage")
	deployCreateCmd.Flags().Bool("skip-approval", false, "Skip manual approval")
	deployCreateCmd.Flags().Bool("auto-rollback", false, "Enable auto-rollback on failure")

	// List flags
	deployListCmd.Flags().String("prompt", "", "Filter by prompt ID")
	deployListCmd.Flags().String("environment", "", "Filter by environment")

	// Approve flags
	deployApproveCmd.Flags().String("comment", "", "Approval comment")

	// Rollback flags
	deployRollbackCmd.Flags().String("reason", "", "Rollback reason")

	// Cancel flags
	deployCancelCmd.Flags().String("reason", "", "Cancel reason")

	// Gate create flags
	deployGateCreateCmd.Flags().String("name", "", "Gate name")
	deployGateCreateCmd.Flags().String("prompt", "", "Prompt ID")
	deployGateCreateCmd.Flags().Bool("required", true, "Gate is required")
	deployGateCreateCmd.Flags().String("condition-type", "eval_score", "Condition type (eval_score, latency, cost)")
	deployGateCreateCmd.Flags().String("operator", "gte", "Operator (gte, lte, eq)")
	deployGateCreateCmd.Flags().Float64("threshold", 0.8, "Threshold value")

	deployCmd.AddCommand(deployCreateCmd)
	deployCmd.AddCommand(deployListCmd)
	deployCmd.AddCommand(deployGetCmd)
	deployCmd.AddCommand(deployApproveCmd)
	deployCmd.AddCommand(deployRollbackCmd)
	deployCmd.AddCommand(deployCancelCmd)
	deployCmd.AddCommand(deployStatusCmd)
	deployCmd.AddCommand(deployGatesCmd)
	deployCmd.AddCommand(deployGateCreateCmd)
}
