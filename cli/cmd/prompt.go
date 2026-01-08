package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
	"github.com/instantcocoa/delos/cli/internal/output"
)

var promptCmd = &cobra.Command{
	Use:   "prompt",
	Short: "Manage prompts",
	Long:  "Commands for creating, updating, and managing prompts.",
}

var promptListCmd = &cobra.Command{
	Use:   "list",
	Short: "List prompts",
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.PromptAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := promptv1.NewPromptServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		tags, _ := cmd.Flags().GetStringSlice("tags")
		search, _ := cmd.Flags().GetString("search")
		limit, _ := cmd.Flags().GetInt32("limit")

		resp, err := client.ListPrompts(ctx, &promptv1.ListPromptsRequest{
			Tags:   tags,
			Search: search,
			Limit:  limit,
		})
		if err != nil {
			return fmt.Errorf("failed to list prompts: %w", err)
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			w := output.NewWriter(cfg.Format)
			return w.Print(resp.Prompts)
		}

		table := output.Table{
			Headers: []string{"ID", "NAME", "SLUG", "VERSION", "UPDATED"},
			Rows:    make([][]string, len(resp.Prompts)),
		}
		for i, p := range resp.Prompts {
			updated := ""
			if p.UpdatedAt != nil {
				updated = p.UpdatedAt.AsTime().Format("2006-01-02 15:04")
			}
			table.Rows[i] = []string{
				p.Id[:8],
				p.Name,
				p.Slug,
				fmt.Sprintf("v%d", p.Version),
				updated,
			}
		}

		w := output.NewWriter("table")
		return w.Print(table)
	},
}

var promptGetCmd = &cobra.Command{
	Use:   "get <id-or-slug>",
	Short: "Get a prompt",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.PromptAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := promptv1.NewPromptServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		reference, _ := cmd.Flags().GetString("reference")

		req := &promptv1.GetPromptRequest{Id: args[0]}
		if reference != "" {
			req.Reference = reference
		}

		resp, err := client.GetPrompt(ctx, req)
		if err != nil {
			return fmt.Errorf("failed to get prompt: %w", err)
		}

		w := output.NewWriter(cfg.Format)
		return w.Print(resp.Prompt)
	},
}

var promptCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new prompt",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.PromptAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := promptv1.NewPromptServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		slug, _ := cmd.Flags().GetString("slug")
		description, _ := cmd.Flags().GetString("description")
		systemPrompt, _ := cmd.Flags().GetString("system")
		userPrompt, _ := cmd.Flags().GetString("user")
		tags, _ := cmd.Flags().GetStringSlice("tags")

		messages := make([]*promptv1.PromptMessage, 0)
		if systemPrompt != "" {
			messages = append(messages, &promptv1.PromptMessage{
				Role:    "system",
				Content: systemPrompt,
			})
		}
		if userPrompt != "" {
			messages = append(messages, &promptv1.PromptMessage{
				Role:    "user",
				Content: userPrompt,
			})
		}

		resp, err := client.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
			Name:        args[0],
			Slug:        slug,
			Description: description,
			Messages:    messages,
			Tags:        tags,
		})
		if err != nil {
			return fmt.Errorf("failed to create prompt: %w", err)
		}

		output.Success("Created prompt %s (ID: %s)", resp.Prompt.Name, resp.Prompt.Id)

		if cfg.Verbose {
			w := output.NewWriter(cfg.Format)
			return w.Print(resp.Prompt)
		}

		return nil
	},
}

var promptUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update a prompt (creates new version)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.PromptAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := promptv1.NewPromptServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		description, _ := cmd.Flags().GetString("description")
		changeDesc, _ := cmd.Flags().GetString("change-description")
		systemPrompt, _ := cmd.Flags().GetString("system")
		userPrompt, _ := cmd.Flags().GetString("user")

		messages := make([]*promptv1.PromptMessage, 0)
		if systemPrompt != "" {
			messages = append(messages, &promptv1.PromptMessage{
				Role:    "system",
				Content: systemPrompt,
			})
		}
		if userPrompt != "" {
			messages = append(messages, &promptv1.PromptMessage{
				Role:    "user",
				Content: userPrompt,
			})
		}

		resp, err := client.UpdatePrompt(ctx, &promptv1.UpdatePromptRequest{
			Id:                args[0],
			Description:       description,
			Messages:          messages,
			ChangeDescription: changeDesc,
		})
		if err != nil {
			return fmt.Errorf("failed to update prompt: %w", err)
		}

		output.Success("Updated prompt %s to v%d", resp.Prompt.Name, resp.Prompt.Version)
		return nil
	},
}

var promptDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a prompt",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.PromptAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := promptv1.NewPromptServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		_, err = client.DeletePrompt(ctx, &promptv1.DeletePromptRequest{
			Id: args[0],
		})
		if err != nil {
			return fmt.Errorf("failed to delete prompt: %w", err)
		}

		output.Success("Deleted prompt %s", args[0])
		return nil
	},
}

var promptHistoryCmd = &cobra.Command{
	Use:   "history <id>",
	Short: "List prompt version history",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.PromptAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := promptv1.NewPromptServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		limit, _ := cmd.Flags().GetInt32("limit")

		resp, err := client.GetPromptHistory(ctx, &promptv1.GetPromptHistoryRequest{
			Id:    args[0],
			Limit: limit,
		})
		if err != nil {
			return fmt.Errorf("failed to get history: %w", err)
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			w := output.NewWriter(cfg.Format)
			return w.Print(resp.Versions)
		}

		table := output.Table{
			Headers: []string{"VERSION", "UPDATED BY", "UPDATED AT", "CHANGE DESCRIPTION"},
			Rows:    make([][]string, len(resp.Versions)),
		}
		for i, v := range resp.Versions {
			updated := ""
			if v.UpdatedAt != nil {
				updated = v.UpdatedAt.AsTime().Format("2006-01-02 15:04")
			}
			changeDesc := v.ChangeDescription
			if len(changeDesc) > 40 {
				changeDesc = changeDesc[:37] + "..."
			}
			table.Rows[i] = []string{
				fmt.Sprintf("v%d", v.Version),
				v.UpdatedBy,
				updated,
				changeDesc,
			}
		}

		w := output.NewWriter("table")
		return w.Print(table)
	},
}

var promptCompareCmd = &cobra.Command{
	Use:   "compare <id> <version-a> <version-b>",
	Short: "Compare two prompt versions",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.PromptAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := promptv1.NewPromptServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		var versionA, versionB int32
		fmt.Sscanf(args[1], "%d", &versionA)
		fmt.Sscanf(args[2], "%d", &versionB)

		resp, err := client.CompareVersions(ctx, &promptv1.CompareVersionsRequest{
			PromptId: args[0],
			VersionA: versionA,
			VersionB: versionB,
		})
		if err != nil {
			return fmt.Errorf("failed to compare versions: %w", err)
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			w := output.NewWriter(cfg.Format)
			return w.Print(resp)
		}

		output.Info("Comparing v%d to v%d", versionA, versionB)
		output.Info("Semantic Similarity: %.2f%%", resp.SemanticSimilarity*100)

		if len(resp.Diffs) > 0 {
			output.Info("\nDifferences:")
			for _, d := range resp.Diffs {
				output.Info("  [%s] %s:", d.DiffType, d.Field)
				if d.OldValue != "" {
					output.Info("    - %s", d.OldValue)
				}
				if d.NewValue != "" {
					output.Info("    + %s", d.NewValue)
				}
			}
		} else {
			output.Info("No differences found.")
		}

		return nil
	},
}

func init() {
	// List flags
	promptListCmd.Flags().StringSlice("tags", nil, "Filter by tags")
	promptListCmd.Flags().String("search", "", "Search in name/description")
	promptListCmd.Flags().Int32("limit", 100, "Maximum results")

	// Get flags
	promptGetCmd.Flags().String("reference", "", "Version reference (e.g., 'summarizer:v2')")

	// Create flags
	promptCreateCmd.Flags().String("slug", "", "URL-friendly name")
	promptCreateCmd.Flags().String("description", "", "Prompt description")
	promptCreateCmd.Flags().String("system", "", "System prompt message")
	promptCreateCmd.Flags().String("user", "", "User prompt template")
	promptCreateCmd.Flags().StringSlice("tags", nil, "Tags")

	// Update flags
	promptUpdateCmd.Flags().String("description", "", "New description")
	promptUpdateCmd.Flags().String("system", "", "New system prompt")
	promptUpdateCmd.Flags().String("user", "", "New user prompt template")
	promptUpdateCmd.Flags().String("change-description", "", "Description of changes")

	// History flags
	promptHistoryCmd.Flags().Int32("limit", 10, "Max versions to show")

	promptCmd.AddCommand(promptListCmd)
	promptCmd.AddCommand(promptGetCmd)
	promptCmd.AddCommand(promptCreateCmd)
	promptCmd.AddCommand(promptUpdateCmd)
	promptCmd.AddCommand(promptDeleteCmd)
	promptCmd.AddCommand(promptHistoryCmd)
	promptCmd.AddCommand(promptCompareCmd)
}
