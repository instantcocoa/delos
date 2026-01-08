package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/durationpb"

	observev1 "github.com/instantcocoa/delos/gen/go/observe/v1"
	"github.com/instantcocoa/delos/cli/internal/output"
)

var observeCmd = &cobra.Command{
	Use:   "observe",
	Short: "Observability operations",
	Long:  "Commands for querying traces and metrics.",
}

var observeTracesCmd = &cobra.Command{
	Use:   "traces",
	Short: "Query traces",
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.ObserveAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := observev1.NewObserveServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		service, _ := cmd.Flags().GetString("service")
		operation, _ := cmd.Flags().GetString("operation")
		minDurationMs, _ := cmd.Flags().GetInt64("min-duration")
		limit, _ := cmd.Flags().GetInt32("limit")

		req := &observev1.QueryTracesRequest{
			ServiceName:   service,
			OperationName: operation,
			Limit:         limit,
		}
		if minDurationMs > 0 {
			req.MinDuration = durationpb.New(time.Duration(minDurationMs) * time.Millisecond)
		}

		resp, err := client.QueryTraces(ctx, req)
		if err != nil {
			return fmt.Errorf("failed to query traces: %w", err)
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			w := output.NewWriter(cfg.Format)
			return w.Print(resp.Traces)
		}

		table := output.Table{
			Headers: []string{"TRACE ID", "SERVICE", "OPERATION", "DURATION", "TIME"},
			Rows:    make([][]string, len(resp.Traces)),
		}
		for i, t := range resp.Traces {
			traceID := t.TraceId
			if len(traceID) > 16 {
				traceID = traceID[:16]
			}
			started := ""
			if t.StartTime != nil {
				started = t.StartTime.AsTime().Format("15:04:05")
			}
			duration := ""
			if t.Duration != nil {
				duration = fmt.Sprintf("%dms", t.Duration.AsDuration().Milliseconds())
			}
			table.Rows[i] = []string{
				traceID,
				t.RootService,
				t.RootOperation,
				duration,
				started,
			}
		}

		w := output.NewWriter("table")
		return w.Print(table)
	},
}

var observeTraceCmd = &cobra.Command{
	Use:   "trace <trace-id>",
	Short: "Get a specific trace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.ObserveAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := observev1.NewObserveServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		resp, err := client.GetTrace(ctx, &observev1.GetTraceRequest{TraceId: args[0]})
		if err != nil {
			return fmt.Errorf("failed to get trace: %w", err)
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			w := output.NewWriter(cfg.Format)
			return w.Print(resp.Trace)
		}

		// Print trace details in a human-readable format
		t := resp.Trace
		output.Info("Trace ID: %s", t.TraceId)
		output.Info("Root Service: %s", t.RootService)
		output.Info("Root Operation: %s", t.RootOperation)
		if t.Duration != nil {
			output.Info("Duration: %dms", t.Duration.AsDuration().Milliseconds())
		}
		if t.StartTime != nil {
			output.Info("Start Time: %s", t.StartTime.AsTime().Format(time.RFC3339))
		}

		if len(t.Spans) > 0 {
			output.Info("\nSpans (%d):", len(t.Spans))
			for _, s := range t.Spans {
				duration := ""
				if s.Duration != nil {
					duration = fmt.Sprintf("%dms", s.Duration.AsDuration().Milliseconds())
				}
				status := "ok"
				if s.Status == observev1.SpanStatus_SPAN_STATUS_ERROR {
					status = "error"
				}
				output.Info("  [%s] %s/%s (%s) - %s", s.SpanId[:8], s.ServiceName, s.Name, duration, status)
			}
		}

		return nil
	},
}

var observeMetricsCmd = &cobra.Command{
	Use:   "metrics",
	Short: "Query metrics",
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.ObserveAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := observev1.NewObserveServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		metricName, _ := cmd.Flags().GetString("name")
		service, _ := cmd.Flags().GetString("service")
		aggregation, _ := cmd.Flags().GetString("aggregation")

		resp, err := client.QueryMetrics(ctx, &observev1.QueryMetricsRequest{
			MetricName:  metricName,
			ServiceName: service,
			Aggregation: aggregation,
		})
		if err != nil {
			return fmt.Errorf("failed to query metrics: %w", err)
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			w := output.NewWriter(cfg.Format)
			return w.Print(resp.DataPoints)
		}

		table := output.Table{
			Headers: []string{"TIMESTAMP", "VALUE"},
			Rows:    make([][]string, len(resp.DataPoints)),
		}
		for i, p := range resp.DataPoints {
			ts := ""
			if p.Timestamp != nil {
				ts = p.Timestamp.AsTime().Format("15:04:05")
			}
			table.Rows[i] = []string{
				ts,
				fmt.Sprintf("%.4f", p.Value),
			}
		}

		w := output.NewWriter("table")
		return w.Print(table)
	},
}

var observeHealthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check observe service health",
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := grpc.NewClient(cfg.ObserveAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		defer conn.Close()

		client := observev1.NewObserveServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		resp, err := client.Health(ctx, &observev1.HealthRequest{})
		if err != nil {
			return fmt.Errorf("health check failed: %w", err)
		}

		output.Success("Observe service is %s (version: %s)", resp.Status, resp.Version)
		return nil
	},
}

func init() {
	// Traces flags
	observeTracesCmd.Flags().String("service", "", "Filter by service name")
	observeTracesCmd.Flags().String("operation", "", "Filter by operation name")
	observeTracesCmd.Flags().Int64("min-duration", 0, "Minimum duration in ms")
	observeTracesCmd.Flags().Int32("limit", 50, "Maximum traces to return")

	// Metrics flags
	observeMetricsCmd.Flags().String("name", "", "Metric name")
	observeMetricsCmd.Flags().String("service", "", "Filter by service name")
	observeMetricsCmd.Flags().String("aggregation", "avg", "Aggregation (sum, avg, min, max, count)")

	observeCmd.AddCommand(observeTracesCmd)
	observeCmd.AddCommand(observeTraceCmd)
	observeCmd.AddCommand(observeMetricsCmd)
	observeCmd.AddCommand(observeHealthCmd)
}
