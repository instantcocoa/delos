package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"

	cpclient "github.com/instantcocoa/delos/cli/internal/client"
	"github.com/instantcocoa/delos/cli/internal/output"
	keysv1 "github.com/instantcocoa/delos/gen/go/keys/v1"
)

// keyCmd manages gateway virtual keys through the control-plane KeyService.
// Keys live in Bifrost's governance config store (Postgres); the control plane
// mints, revokes and reports usage, and the gateway enforces budgets, rate
// limits and expiry from the same tables.
var keyCmd = &cobra.Command{
	Use:   "key",
	Short: "Manage gateway virtual keys",
	Long: `Create, list, revoke and inspect virtual keys for delos-gateway.

Keys are minted by the control plane (delos serve) and stored in Bifrost's
governance config store. The plaintext key is shown exactly once, at creation.`,
}

func keyClient(cmd *cobra.Command) (keysv1.KeyServiceClient, func(), error) {
	conn, err := cpclient.Dial(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect: %w", err)
	}
	return keysv1.NewKeyServiceClient(conn), func() { conn.Close() }, nil
}

func keyTimeout(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	return context.WithTimeout(cmd.Context(), 30*time.Second)
}

var keyCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a virtual key (the key is printed once)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		models, _ := cmd.Flags().GetStringSlice("models")
		tokenBudget, _ := cmd.Flags().GetInt64("token-budget")
		usdBudget, _ := cmd.Flags().GetFloat64("usd-budget")
		dailyBudget, _ := cmd.Flags().GetFloat64("daily-budget")
		totalBudget, _ := cmd.Flags().GetFloat64("total-budget")
		sessionID, _ := cmd.Flags().GetString("session-id")
		orgID, _ := cmd.Flags().GetString("org-id")
		subject, _ := cmd.Flags().GetString("subject")
		labels, _ := cmd.Flags().GetStringToString("label")
		expiresAt, _ := cmd.Flags().GetString("expires-at")

		req := &keysv1.MintKeyRequest{
			Name:      args[0],
			Models:    models,
			SessionId: sessionID,
			OrgId:     orgID,
			Subject:   subject,
			Labels:    labels,
		}
		if usdBudget > 0 {
			req.Budgets = append(req.Budgets, &keysv1.Budget{MaxLimitUsd: usdBudget, ResetDuration: "1M"})
		}
		if dailyBudget > 0 {
			req.Budgets = append(req.Budgets, &keysv1.Budget{MaxLimitUsd: dailyBudget, ResetDuration: "1d"})
		}
		if totalBudget > 0 {
			req.Budgets = append(req.Budgets, &keysv1.Budget{MaxLimitUsd: totalBudget, ResetDuration: ""})
		}
		if tokenBudget > 0 {
			req.RateLimit = &keysv1.RateLimit{TokenMaxLimit: tokenBudget, TokenResetDuration: "1M"}
		}
		if expiresAt != "" {
			t, err := time.Parse(time.RFC3339, expiresAt)
			if err != nil {
				return fmt.Errorf("invalid --expires-at %q: use RFC 3339, e.g. 2026-12-31T23:59:59Z: %w", expiresAt, err)
			}
			req.ExpiresAt = timestamppb.New(t)
		}

		client, close, err := keyClient(cmd)
		if err != nil {
			return err
		}
		defer close()
		ctx, cancel := keyTimeout(cmd)
		defer cancel()

		resp, err := client.MintKey(ctx, req)
		if err != nil {
			return fmt.Errorf("failed to create key: %w", err)
		}

		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "Created key %q (id %s)\n\n", resp.Key.Name, resp.Key.Id)
		fmt.Fprintf(out, "  %s\n\n", resp.Secret)
		fmt.Fprintln(out, "Store this key now - it is shown only once and cannot be recovered.")
		return nil
	},
}

var keyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List virtual keys",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, close, err := keyClient(cmd)
		if err != nil {
			return err
		}
		defer close()
		ctx, cancel := keyTimeout(cmd)
		defer cancel()

		resp, err := client.ListKeys(ctx, &keysv1.ListKeysRequest{})
		if err != nil {
			return fmt.Errorf("failed to list keys: %w", err)
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			return output.NewWriter(cfg.Format).Print(resp.Keys)
		}

		table := output.Table{
			Headers: []string{"ID", "NAME", "MODELS", "BUDGETS", "EXPIRES", "ACTIVE"},
		}
		for _, k := range resp.Keys {
			models := strings.Join(k.Models, ",")
			if models == "" {
				models = "*"
			}
			budgets := "unlimited"
			if len(k.Budgets) > 0 {
				parts := make([]string, 0, len(k.Budgets))
				for _, b := range k.Budgets {
					window := b.ResetDuration
					if window == "" {
						window = "total"
					}
					parts = append(parts, fmt.Sprintf("$%.2f/%s", b.MaxLimitUsd, window))
				}
				budgets = strings.Join(parts, ", ")
			}
			expires := "never"
			if k.ExpiresAt != nil {
				expires = k.ExpiresAt.AsTime().Format(time.RFC3339)
			}
			table.Rows = append(table.Rows, []string{
				shortID(k.Id, 8), k.Name, models, budgets, expires, fmt.Sprintf("%v", k.Active),
			})
		}
		return output.NewWriter("table").Print(table)
	},
}

var keyRevokeCmd = &cobra.Command{
	Use:   "revoke <id>",
	Short: "Revoke a virtual key",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, close, err := keyClient(cmd)
		if err != nil {
			return err
		}
		defer close()
		ctx, cancel := keyTimeout(cmd)
		defer cancel()

		if _, err := client.RevokeKey(ctx, &keysv1.RevokeKeyRequest{Id: args[0]}); err != nil {
			return fmt.Errorf("failed to revoke key %s: %w", args[0], err)
		}
		cmd.Printf("Revoked key %s (gateway replicas observe the change within one second)\n", args[0])
		return nil
	},
}

var keyUsageCmd = &cobra.Command{
	Use:   "usage <id>",
	Short: "Show a key's budget and rate-limit usage",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, close, err := keyClient(cmd)
		if err != nil {
			return err
		}
		defer close()
		ctx, cancel := keyTimeout(cmd)
		defer cancel()

		resp, err := client.GetKeyUsage(ctx, &keysv1.GetKeyUsageRequest{Id: args[0]})
		if err != nil {
			return fmt.Errorf("failed to get key usage: %w", err)
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			return output.NewWriter(cfg.Format).Print(resp)
		}

		table := output.Table{Headers: []string{"LIMIT", "MAX", "USED", "RESET"}}
		for _, b := range resp.Budgets {
			table.Rows = append(table.Rows, []string{"budget", fmt.Sprintf("$%.2f", b.MaxLimitUsd),
				fmt.Sprintf("$%.4f", b.CurrentUsageUsd), b.ResetDuration})
		}
		if resp.RateLimit != nil {
			rl := resp.RateLimit
			if rl.TokenMaxLimit > 0 {
				table.Rows = append(table.Rows, []string{"tokens", fmt.Sprintf("%d", rl.TokenMaxLimit),
					fmt.Sprintf("%d", rl.TokenCurrentUsage), ""})
			}
			if rl.RequestMaxLimit > 0 {
				table.Rows = append(table.Rows, []string{"requests", fmt.Sprintf("%d", rl.RequestMaxLimit),
					fmt.Sprintf("%d", rl.RequestCurrentUsage), ""})
			}
		}
		return output.NewWriter("table").Print(table)
	},
}

func init() {
	keyCreateCmd.Flags().StringSlice("models", nil, "Models this key may use (default: all; supports provider/* wildcards)")
	keyCreateCmd.Flags().Int64("token-budget", 0, "Monthly token budget (0 = unlimited; stored as a monthly rate limit)")
	keyCreateCmd.Flags().Float64("usd-budget", 0, "Monthly dollar budget (0 = unlimited)")
	keyCreateCmd.Flags().Float64("daily-budget", 0, "Daily dollar budget (0 = unlimited)")
	keyCreateCmd.Flags().Float64("total-budget", 0, "Total dollar budget over the key's life (0 = unlimited)")
	keyCreateCmd.Flags().String("session-id", "", "Session id this key belongs to (stamped as gen_ai.conversation.id)")
	keyCreateCmd.Flags().String("org-id", "", "Org id this key belongs to (stamped as delos.org_id)")
	keyCreateCmd.Flags().String("subject", "", "Subject this key acts for (stamped as delos.subject)")
	keyCreateCmd.Flags().StringToString("label", nil, "Label for cost attribution (key=value, repeatable)")
	keyCreateCmd.Flags().String("expires-at", "", "Expiry in RFC 3339 (e.g. 2026-12-31T23:59:59Z)")

	keyCmd.AddCommand(keyCreateCmd)
	keyCmd.AddCommand(keyListCmd)
	keyCmd.AddCommand(keyRevokeCmd)
	keyCmd.AddCommand(keyUsageCmd)
	rootCmd.AddCommand(keyCmd)
}
