package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	datasetsv1 "github.com/instantcocoa/delos/gen/go/datasets/v1"
	"github.com/instantcocoa/delos/cli/internal/output"
)

var datasetsCmd = &cobra.Command{
	Use:     "datasets",
	Aliases: []string{"dataset", "ds"},
	Short:   "Manage datasets",
	Long:    "Commands for creating and managing evaluation datasets.",
}

var datasetsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List datasets",
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.DatasetsAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := datasetsv1.NewDatasetsServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		promptID, _ := cmd.Flags().GetString("prompt")
		tags, _ := cmd.Flags().GetStringSlice("tags")
		search, _ := cmd.Flags().GetString("search")

		resp, err := client.ListDatasets(ctx, &datasetsv1.ListDatasetsRequest{
			PromptId: promptID,
			Tags:     tags,
			Search:   search,
			Limit:    100,
		})
		if err != nil {
			return fmt.Errorf("failed to list datasets: %w", err)
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			w := output.NewWriter(cfg.Format)
			return w.Print(resp.Datasets)
		}

		table := output.Table{
			Headers: []string{"ID", "NAME", "PROMPT", "EXAMPLES", "UPDATED"},
			Rows:    make([][]string, len(resp.Datasets)),
		}
		for i, d := range resp.Datasets {
			updated := ""
			if d.LastUpdated != nil {
				updated = d.LastUpdated.AsTime().Format("2006-01-02 15:04")
			}
			promptID := d.PromptId
			if len(promptID) > 8 {
				promptID = promptID[:8]
			}
			table.Rows[i] = []string{
				d.Id[:8],
				d.Name,
				promptID,
				fmt.Sprintf("%d", d.ExampleCount),
				updated,
			}
		}

		w := output.NewWriter("table")
		return w.Print(table)
	},
}

var datasetsGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Get dataset details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.DatasetsAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := datasetsv1.NewDatasetsServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		resp, err := client.GetDataset(ctx, &datasetsv1.GetDatasetRequest{Id: args[0]})
		if err != nil {
			return fmt.Errorf("failed to get dataset: %w", err)
		}

		w := output.NewWriter(cfg.Format)
		return w.Print(resp.Dataset)
	},
}

var datasetsCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new dataset",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.DatasetsAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := datasetsv1.NewDatasetsServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		description, _ := cmd.Flags().GetString("description")
		promptID, _ := cmd.Flags().GetString("prompt")
		tags, _ := cmd.Flags().GetStringSlice("tags")

		resp, err := client.CreateDataset(ctx, &datasetsv1.CreateDatasetRequest{
			Name:        args[0],
			Description: description,
			PromptId:    promptID,
			Tags:        tags,
		})
		if err != nil {
			return fmt.Errorf("failed to create dataset: %w", err)
		}

		output.Success("Created dataset %s (ID: %s)", resp.Dataset.Name, resp.Dataset.Id)
		return nil
	},
}

var datasetsDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a dataset",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.DatasetsAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := datasetsv1.NewDatasetsServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		_, err = client.DeleteDataset(ctx, &datasetsv1.DeleteDatasetRequest{Id: args[0]})
		if err != nil {
			return fmt.Errorf("failed to delete dataset: %w", err)
		}

		output.Success("Deleted dataset %s", args[0])
		return nil
	},
}

var datasetsExamplesCmd = &cobra.Command{
	Use:   "examples <dataset-id>",
	Short: "List examples in a dataset",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.DatasetsAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := datasetsv1.NewDatasetsServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		limit, _ := cmd.Flags().GetInt32("limit")
		shuffle, _ := cmd.Flags().GetBool("shuffle")

		resp, err := client.GetExamples(ctx, &datasetsv1.GetExamplesRequest{
			DatasetId: args[0],
			Limit:     limit,
			Shuffle:   shuffle,
		})
		if err != nil {
			return fmt.Errorf("failed to get examples: %w", err)
		}

		output.Info("Found %d examples (showing %d)", resp.TotalCount, len(resp.Examples))

		w := output.NewWriter(cfg.Format)
		return w.Print(resp.Examples)
	},
}

func init() {
	// List flags
	datasetsListCmd.Flags().String("prompt", "", "Filter by prompt ID")
	datasetsListCmd.Flags().StringSlice("tags", nil, "Filter by tags")
	datasetsListCmd.Flags().String("search", "", "Search in name/description")

	// Create flags
	datasetsCreateCmd.Flags().String("description", "", "Dataset description")
	datasetsCreateCmd.Flags().String("prompt", "", "Linked prompt ID")
	datasetsCreateCmd.Flags().StringSlice("tags", nil, "Tags")

	// Examples flags
	datasetsExamplesCmd.Flags().Int32("limit", 10, "Max examples to show")
	datasetsExamplesCmd.Flags().Bool("shuffle", false, "Shuffle examples")

	datasetsCmd.AddCommand(datasetsListCmd)
	datasetsCmd.AddCommand(datasetsGetCmd)
	datasetsCmd.AddCommand(datasetsCreateCmd)
	datasetsCmd.AddCommand(datasetsDeleteCmd)
	datasetsCmd.AddCommand(datasetsExamplesCmd)
}
