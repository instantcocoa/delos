package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"

	cpclient "github.com/instantcocoa/delos/cli/internal/client"
	"github.com/instantcocoa/delos/cli/internal/output"
	datasetsv1 "github.com/instantcocoa/delos/gen/go/datasets/v1"
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
		conn, err := cpclient.Dial(cfg)
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
			promptID := shortID(d.PromptId, 8)
			table.Rows[i] = []string{
				shortID(d.Id, 8),
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
		conn, err := cpclient.Dial(cfg)
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
		conn, err := cpclient.Dial(cfg)
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
		conn, err := cpclient.Dial(cfg)
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
		conn, err := cpclient.Dial(cfg)
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

		if len(resp.Examples) == 0 {
			output.Info("Dataset %s has no examples yet. Add some with `delos datasets add %s --example 'input=>expected'`.", args[0], args[0])
			return nil
		}

		output.Info("Found %d examples (showing %d)", resp.TotalCount, len(resp.Examples))

		w := output.NewWriter(cfg.Format)
		return w.Print(resp.Examples)
	},
}

// dataFormatFromName maps a user-facing format name to the proto enum.
func dataFormatFromName(name string) (datasetsv1.DataFormat, error) {
	switch strings.ToLower(strings.TrimPrefix(name, ".")) {
	case "json":
		return datasetsv1.DataFormat_DATA_FORMAT_JSON, nil
	case "jsonl", "ndjson":
		return datasetsv1.DataFormat_DATA_FORMAT_JSONL, nil
	case "csv":
		return datasetsv1.DataFormat_DATA_FORMAT_CSV, nil
	default:
		return datasetsv1.DataFormat_DATA_FORMAT_UNSPECIFIED, fmt.Errorf("unsupported format %q (use json, jsonl or csv)", name)
	}
}

// exampleField converts one side of an --example pair into a struct. A JSON
// object is used as-is; anything else becomes {defaultKey: <text>}.
func exampleField(text, defaultKey string) (*structpb.Struct, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("empty value")
	}
	if strings.HasPrefix(text, "{") {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(text), &m); err != nil {
			return nil, fmt.Errorf("invalid JSON object %q: %w", text, err)
		}
		return structpb.NewStruct(m)
	}
	return structpb.NewStruct(map[string]interface{}{defaultKey: text})
}

// parseExampleFlag parses "input=>expected" into an ExampleInput.
func parseExampleFlag(pair string) (*datasetsv1.ExampleInput, error) {
	left, right, found := strings.Cut(pair, "=>")
	if !found {
		return nil, fmt.Errorf("example %q must be written as 'input=>expected'", pair)
	}

	input, err := exampleField(left, "input")
	if err != nil {
		return nil, fmt.Errorf("example %q: input: %w", pair, err)
	}
	expected, err := exampleField(right, "output")
	if err != nil {
		return nil, fmt.Errorf("example %q: expected output: %w", pair, err)
	}

	return &datasetsv1.ExampleInput{
		Input:          input,
		ExpectedOutput: expected,
		Source:         datasetsv1.ExampleSource_EXAMPLE_SOURCE_MANUAL,
	}, nil
}

var datasetsAddCmd = &cobra.Command{
	Use:   "add <dataset-id>",
	Short: "Add examples to a dataset",
	Long: `Add examples to a dataset, either inline or from a file.

Inline examples are written as 'input=>expected'. Each side is used as a JSON
object when it starts with "{", otherwise it becomes {"input": ...} on the
left and {"output": ...} on the right.

  delos datasets add ds_1 --example 'hello=>Hello!' --example 'bye=>Goodbye!'
  delos datasets add ds_1 --example '{"text":"hi"}=>{"output":"Hello!"}'
  delos datasets add ds_1 --file cases.jsonl`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		examples, _ := cmd.Flags().GetStringArray("example")
		file, _ := cmd.Flags().GetString("file")
		formatName, _ := cmd.Flags().GetString("format")
		skipInvalid, _ := cmd.Flags().GetBool("skip-invalid")

		if len(examples) == 0 && file == "" {
			return fmt.Errorf("provide at least one --example 'input=>expected' or a --file to import")
		}
		if len(examples) > 0 && file != "" {
			return fmt.Errorf("--example and --file are mutually exclusive")
		}

		conn, err := cpclient.Dial(cfg)
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := datasetsv1.NewDatasetsServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		if file != "" {
			if formatName == "" {
				formatName = filepath.Ext(file)
			}
			if formatName == "" {
				return fmt.Errorf("cannot infer format from %q; pass --format json|jsonl|csv", file)
			}
			format, err := dataFormatFromName(formatName)
			if err != nil {
				return err
			}

			data, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("failed to read %s: %w", file, err)
			}

			resp, err := client.ImportExamples(ctx, &datasetsv1.ImportExamplesRequest{
				DatasetId: args[0],
				Source: &datasetsv1.DataSource{
					Source: &datasetsv1.DataSource_Inline{
						Inline: &datasetsv1.InlineSource{Data: data, Format: format},
					},
				},
				Format:      format,
				SkipInvalid: skipInvalid,
			})
			if err != nil {
				return fmt.Errorf("failed to import examples: %w", err)
			}

			output.Success("Imported %d example(s) into %s from %s", resp.ImportedCount, args[0], file)
			if resp.ErrorCount > 0 {
				output.Info("Skipped %d row(s):", resp.ErrorCount)
				for _, e := range resp.Errors {
					output.Info("  row %d: %s", e.RowNumber, e.ErrorMessage)
				}
			}
			if cfg.Format == "json" || cfg.Format == "yaml" {
				return output.NewWriter(cfg.Format).Print(resp)
			}
			return nil
		}

		inputs := make([]*datasetsv1.ExampleInput, 0, len(examples))
		for _, pair := range examples {
			ex, err := parseExampleFlag(pair)
			if err != nil {
				return err
			}
			inputs = append(inputs, ex)
		}

		resp, err := client.AddExamples(ctx, &datasetsv1.AddExamplesRequest{
			DatasetId: args[0],
			Examples:  inputs,
		})
		if err != nil {
			return fmt.Errorf("failed to add examples: %w", err)
		}

		output.Success("Added %d example(s) to dataset %s", resp.AddedCount, args[0])
		if cfg.Format == "json" || cfg.Format == "yaml" {
			return output.NewWriter(cfg.Format).Print(resp.Examples)
		}
		return nil
	},
}

var datasetsExportCmd = &cobra.Command{
	Use:   "export <dataset-id>",
	Short: "Export a dataset's examples",
	Long: `Export a dataset's examples as JSON, JSONL or CSV.

The data is written to stdout unless --file is given. (The global -o/--output
flag still selects the CLI's own output format, so the export format has its
own --format flag.)

  delos datasets export ds_1 --format jsonl
  delos datasets export ds_1 --format csv --file cases.csv`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		formatName, _ := cmd.Flags().GetString("format")
		file, _ := cmd.Flags().GetString("file")
		limit, _ := cmd.Flags().GetInt32("limit")

		format, err := dataFormatFromName(formatName)
		if err != nil {
			return err
		}

		conn, err := cpclient.Dial(cfg)
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := datasetsv1.NewDatasetsServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		resp, err := client.ExportExamples(ctx, &datasetsv1.ExportExamplesRequest{
			DatasetId: args[0],
			Format:    format,
			Limit:     limit,
		})
		if err != nil {
			return fmt.Errorf("failed to export examples: %w", err)
		}

		if file != "" {
			if err := os.WriteFile(file, resp.Data, 0o644); err != nil {
				return fmt.Errorf("failed to write %s: %w", file, err)
			}
			output.Success("Exported %d example(s) to %s", resp.ExportedCount, file)
			return nil
		}

		if _, err := cmd.OutOrStdout().Write(resp.Data); err != nil {
			return err
		}
		if len(resp.Data) > 0 && resp.Data[len(resp.Data)-1] != '\n' {
			fmt.Fprintln(cmd.OutOrStdout())
		}
		return nil
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

	// Add flags
	datasetsAddCmd.Flags().StringArray("example", nil, "Inline example 'input=>expected', repeatable")
	datasetsAddCmd.Flags().String("file", "", "Import examples from a json, jsonl or csv file")
	datasetsAddCmd.Flags().String("format", "", "Format of --file (default: inferred from the extension)")
	datasetsAddCmd.Flags().Bool("skip-invalid", true, "Skip rows that fail validation instead of failing the import")

	// Export flags
	datasetsExportCmd.Flags().String("format", "json", "Export format: json, jsonl or csv")
	datasetsExportCmd.Flags().String("file", "", "Write to this file instead of stdout")
	datasetsExportCmd.Flags().Int32("limit", 0, "Max examples to export (0 = all)")

	datasetsCmd.AddCommand(datasetsAddCmd)
	datasetsCmd.AddCommand(datasetsExportCmd)
}
