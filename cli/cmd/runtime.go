package cmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	runtimev1 "github.com/instantcocoa/delos/gen/go/runtime/v1"
	"github.com/instantcocoa/delos/cli/internal/output"
)

var runtimeCmd = &cobra.Command{
	Use:   "runtime",
	Short: "Runtime operations",
	Long:  "Commands for completions and model management.",
}

var runtimeCompleteCmd = &cobra.Command{
	Use:   "complete <message>",
	Short: "Generate a completion",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.RuntimeAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := runtimev1.NewRuntimeServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		model, _ := cmd.Flags().GetString("model")
		provider, _ := cmd.Flags().GetString("provider")
		promptRef, _ := cmd.Flags().GetString("prompt-ref")
		system, _ := cmd.Flags().GetString("system")
		temperature, _ := cmd.Flags().GetFloat64("temperature")
		maxTokens, _ := cmd.Flags().GetInt32("max-tokens")
		stream, _ := cmd.Flags().GetBool("stream")

		messages := make([]*runtimev1.Message, 0)
		if system != "" {
			messages = append(messages, &runtimev1.Message{
				Role:    "system",
				Content: system,
			})
		}
		messages = append(messages, &runtimev1.Message{
			Role:    "user",
			Content: args[0],
		})

		params := &runtimev1.CompletionParams{
			PromptRef:   promptRef,
			Messages:    messages,
			Provider:    provider,
			Model:       model,
			Temperature: temperature,
			MaxTokens:   maxTokens,
		}

		if stream {
			streamReq := &runtimev1.CompleteStreamRequest{Params: params}
			streamClient, err := client.CompleteStream(ctx, streamReq)
			if err != nil {
				return fmt.Errorf("failed to start stream: %w", err)
			}

			for {
				resp, err := streamClient.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("stream error: %w", err)
				}
				fmt.Print(resp.Delta)
			}
			fmt.Println()
			return nil
		}

		resp, err := client.Complete(ctx, &runtimev1.CompleteRequest{Params: params})
		if err != nil {
			return fmt.Errorf("failed to complete: %w", err)
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			w := output.NewWriter(cfg.Format)
			return w.Print(resp)
		}

		fmt.Println(resp.Content)
		if cfg.Verbose && resp.Usage != nil {
			fmt.Printf("\n---\nProvider: %s | Model: %s | Tokens: %d | Cost: $%.4f\n",
				resp.Provider, resp.Model, resp.Usage.TotalTokens, resp.Usage.CostUsd)
		}
		return nil
	},
}

var runtimeProvidersCmd = &cobra.Command{
	Use:   "providers",
	Short: "List available providers",
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.RuntimeAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := runtimev1.NewRuntimeServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		resp, err := client.ListProviders(ctx, &runtimev1.ListProvidersRequest{})
		if err != nil {
			return fmt.Errorf("failed to list providers: %w", err)
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			w := output.NewWriter(cfg.Format)
			return w.Print(resp.Providers)
		}

		table := output.Table{
			Headers: []string{"NAME", "MODELS", "AVAILABLE"},
			Rows:    make([][]string, len(resp.Providers)),
		}
		for i, p := range resp.Providers {
			status := "yes"
			if !p.Available {
				status = "no"
			}
			models := fmt.Sprintf("%d", len(p.Models))
			table.Rows[i] = []string{
				p.Name,
				models,
				status,
			}
		}

		w := output.NewWriter("table")
		return w.Print(table)
	},
}

var runtimeEmbedCmd = &cobra.Command{
	Use:   "embed <text>",
	Short: "Generate embeddings for text",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.RuntimeAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := runtimev1.NewRuntimeServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		model, _ := cmd.Flags().GetString("model")
		provider, _ := cmd.Flags().GetString("provider")

		resp, err := client.Embed(ctx, &runtimev1.EmbedRequest{
			Texts:    args,
			Model:    model,
			Provider: provider,
		})
		if err != nil {
			return fmt.Errorf("failed to embed: %w", err)
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			w := output.NewWriter(cfg.Format)
			return w.Print(resp)
		}

		output.Info("Generated %d embeddings", len(resp.Embeddings))
		output.Info("Model: %s | Provider: %s", resp.Model, resp.Provider)
		if resp.Usage != nil {
			output.Info("Tokens: %d | Cost: $%.4f", resp.Usage.TotalTokens, resp.Usage.CostUsd)
		}
		for i, e := range resp.Embeddings {
			output.Info("Embedding %d: %d dimensions", i+1, e.Dimensions)
		}

		return nil
	},
}

var runtimeHealthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check runtime service health",
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.RuntimeAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := runtimev1.NewRuntimeServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		resp, err := client.Health(ctx, &runtimev1.HealthRequest{})
		if err != nil {
			return fmt.Errorf("health check failed: %w", err)
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			w := output.NewWriter(cfg.Format)
			return w.Print(resp)
		}

		output.Success("Runtime service is %s (version: %s)", resp.Status, resp.Version)
		if len(resp.ProviderStatus) > 0 {
			output.Info("\nProvider Status:")
			for name, available := range resp.ProviderStatus {
				status := "available"
				if !available {
					status = "unavailable"
				}
				output.Info("  %s: %s", name, status)
			}
		}
		return nil
	},
}

func init() {
	// Complete flags
	runtimeCompleteCmd.Flags().String("model", "", "Model to use")
	runtimeCompleteCmd.Flags().String("provider", "", "Provider to use")
	runtimeCompleteCmd.Flags().String("prompt-ref", "", "Prompt reference (e.g., 'summarizer:v2')")
	runtimeCompleteCmd.Flags().String("system", "", "System prompt")
	runtimeCompleteCmd.Flags().Float64("temperature", 0.7, "Temperature")
	runtimeCompleteCmd.Flags().Int32("max-tokens", 1024, "Max tokens")
	runtimeCompleteCmd.Flags().Bool("stream", false, "Stream response")

	// Embed flags
	runtimeEmbedCmd.Flags().String("model", "", "Embedding model to use")
	runtimeEmbedCmd.Flags().String("provider", "", "Provider to use")

	runtimeCmd.AddCommand(runtimeCompleteCmd)
	runtimeCmd.AddCommand(runtimeProvidersCmd)
	runtimeCmd.AddCommand(runtimeEmbedCmd)
	runtimeCmd.AddCommand(runtimeHealthCmd)
}
