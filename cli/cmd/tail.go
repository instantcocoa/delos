package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/instantcocoa/delos/services/runtime"
)

// `delos tail` is the demo feature and the debugging feature: one line per
// request through the gateway, live. It reads the gateway's SSE stream at
// GET /v1/events, so `curl -N $DELOS_GATEWAY_URL/v1/events` shows the same
// data without the CLI.

var (
	tailJSON    bool
	tailReplay  int
	tailNoRetry bool
)

var tailCmd = &cobra.Command{
	Use:   "tail",
	Short: "Live-stream requests flowing through the gateway",
	Long: `Stream one line per request handled by delos-gateway: model, provider,
status, latency, tokens, cost, cache hit and virtual key.

The stream comes from GET /v1/events on the gateway (DELOS_GATEWAY_URL,
default http://localhost:8080). Press Ctrl-C to stop.

Examples:
  delos tail                  # live table
  delos tail --replay 20      # the last 20 requests first, then live
  delos tail --json | jq .    # raw events, one JSON object per line`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true

		ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		path := "/v1/events"
		if tailReplay > 0 {
			path = fmt.Sprintf("%s?replay=%d", path, tailReplay)
		}
		url := strings.TrimRight(cfg.GatewayURL, "/") + path

		printed := false
		for {
			err := streamTail(ctx, cmd.OutOrStdout(), url, &printed)
			if ctx.Err() != nil {
				return nil
			}
			if err != nil {
				if tailNoRetry {
					cmd.PrintErrln(err.Error())
					return errSilentExit
				}
				cmd.PrintErrf("%v - reconnecting in 2s\n", err)
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(2 * time.Second):
			}
			// Replay only applies to the first connection; after a reconnect
			// the recent events have already been shown.
			url = strings.TrimRight(cfg.GatewayURL, "/") + "/v1/events"
		}
	},
}

// streamTail consumes one SSE connection until it ends or the context is done.
func streamTail(ctx context.Context, out io.Writer, url string, printedHeader *bool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	if key := os.Getenv("DELOS_API_KEY"); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	// No client timeout: this connection is meant to stay open.
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("cannot reach the gateway at %s: %w", cfg.GatewayURL, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return fmt.Errorf("this gateway is not streaming events (start it without DELOS_TAIL=off)")
	case http.StatusUnauthorized:
		return fmt.Errorf("the gateway rejected the request: set DELOS_API_KEY to a virtual key (see `delos key create`)")
	default:
		return fmt.Errorf("gateway returned %s", resp.Status)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue // keepalive comment or blank separator
		}
		if tailJSON {
			fmt.Fprintln(out, data)
			continue
		}
		var ev runtime.TailEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		if !*printedHeader {
			fmt.Fprintln(out, tailHeader())
			*printedHeader = true
		}
		fmt.Fprintln(out, tailLine(ev))
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("stream ended: %w", err)
	}
	if ctx.Err() != nil {
		return nil
	}
	return fmt.Errorf("the gateway closed the stream")
}

// Column layout, shared by the header and the rows.
const tailRowFormat = "%-8s  %-6s  %-24s  %-10s  %8s  %11s  %10s  %-5s  %s"

func tailHeader() string {
	return fmt.Sprintf(tailRowFormat,
		"TIME", "STATUS", "MODEL", "PROVIDER", "LATENCY", "TOKENS", "COST", "CACHE", "KEY")
}

func tailLine(ev runtime.TailEvent) string {
	status := fmt.Sprintf("%d", ev.Status)
	if ev.Status == 0 {
		status = "-"
	}
	cache := "-"
	if ev.CacheHit {
		cache = "hit"
	}
	tokens := "-"
	if ev.PromptTokens > 0 || ev.CompletionTokens > 0 {
		tokens = fmt.Sprintf("%d->%d", ev.PromptTokens, ev.CompletionTokens)
	}
	key := ev.KeyName
	if key == "" {
		key = "-"
	}
	line := fmt.Sprintf(tailRowFormat,
		ev.Time.Local().Format("15:04:05"),
		status,
		truncate(orDash(ev.Model), 24),
		truncate(orDash(ev.Provider), 10),
		humanDuration(ev.LatencyMS),
		tokens,
		humanCost(ev.CostUSD),
		cache,
		key,
	)
	if ev.Error != "" {
		line += "  " + truncate(ev.Error, 80)
	}
	return line
}

// humanDuration renders milliseconds the way a person reads them.
func humanDuration(ms float64) string {
	switch {
	case ms <= 0:
		return "-"
	case ms < 1:
		return fmt.Sprintf("%.0fus", ms*1000)
	case ms < 1000:
		return fmt.Sprintf("%.0fms", ms)
	case ms < 60_000:
		return fmt.Sprintf("%.1fs", ms/1000)
	default:
		return fmt.Sprintf("%.1fm", ms/60_000)
	}
}

// humanCost keeps small per-request costs readable without losing them to
// rounding.
func humanCost(usd float64) string {
	switch {
	case usd == 0:
		return "-"
	case usd < 0.01:
		return fmt.Sprintf("$%.5f", usd)
	case usd < 1:
		return fmt.Sprintf("$%.4f", usd)
	default:
		return fmt.Sprintf("$%.2f", usd)
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func init() {
	tailCmd.Flags().BoolVar(&tailJSON, "json", false, "Emit raw JSON events, one per line")
	tailCmd.Flags().IntVar(&tailReplay, "replay", 0, "Show this many recent requests before going live")
	tailCmd.Flags().BoolVar(&tailNoRetry, "no-retry", false, "Exit instead of reconnecting when the stream drops")
	rootCmd.AddCommand(tailCmd)
}
