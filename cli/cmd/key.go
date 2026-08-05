package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/instantcocoa/delos/cli/internal/output"
	pkgconfig "github.com/instantcocoa/delos/pkg/config"
	"github.com/instantcocoa/delos/pkg/database"
	"github.com/instantcocoa/delos/services/runtime"
)

// keyCmd manages gateway virtual keys. Keys live in Postgres (the gateway
// reads them there); these commands talk to the database directly using the
// standard DELOS_DB_* configuration.
var keyCmd = &cobra.Command{
	Use:   "key",
	Short: "Manage gateway virtual keys",
	Long: `Create, list, and revoke virtual keys for delos-gateway.

Keys are stored in Postgres as argon2id hashes; the plaintext key is shown
exactly once, at creation. Configure the database with the DELOS_DB_*
environment variables (same as the gateway and control plane).`,
}

func openKeyStore(ctx context.Context) (*runtime.PostgresKeyStore, func(), error) {
	base, err := pkgconfig.Load("delos-cli")
	if err != nil {
		return nil, nil, err
	}
	db, err := database.Connect(ctx, &database.Config{
		Host:            base.DBHost,
		Port:            base.DBPort,
		User:            base.DBUser,
		Password:        base.DBPassword,
		Database:        base.DBName,
		SSLMode:         base.DBSSLMode,
		MaxOpenConns:    2,
		MaxIdleConns:    1,
		ConnMaxLifetime: time.Minute,
		ConnMaxIdleTime: time.Minute,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("could not connect to Postgres (set DELOS_DB_HOST etc.): %w", err)
	}
	migrator := database.NewMigrator(db, "gateway")
	if err := migrator.LoadMigrations(runtime.Migrations, "migrations"); err != nil {
		db.Close()
		return nil, nil, err
	}
	if err := migrator.Up(ctx); err != nil {
		db.Close()
		return nil, nil, err
	}
	return runtime.NewPostgresKeyStore(db.DB), func() { db.Close() }, nil
}

var keyCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a virtual key (the key is printed once)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		models, _ := cmd.Flags().GetStringSlice("models")
		tokenBudget, _ := cmd.Flags().GetInt64("token-budget")
		usdBudget, _ := cmd.Flags().GetFloat64("usd-budget")

		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()
		store, closeDB, err := openKeyStore(ctx)
		if err != nil {
			return err
		}
		defer closeDB()

		secret, err := runtime.GenerateKey()
		if err != nil {
			return err
		}
		hash, err := runtime.HashKey(secret)
		if err != nil {
			return err
		}
		key := &runtime.VirtualKey{
			ID:          uuid.NewString(),
			Name:        args[0],
			Prefix:      runtime.KeyPrefix(secret),
			Hash:        hash,
			Models:      models,
			TokenBudget: tokenBudget,
			USDBudget:   usdBudget,
			CreatedAt:   time.Now().UTC(),
		}
		if err := store.CreateKey(ctx, key); err != nil {
			return fmt.Errorf("failed to create key: %w", err)
		}

		// The secret goes to stdout so it can be captured by scripts; the
		// banner goes around it. It is shown exactly once.
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "Created key %q (id %s)\n\n", key.Name, key.ID)
		fmt.Fprintf(out, "  %s\n\n", secret)
		fmt.Fprintln(out, "Store this key now - it is shown only once and cannot be recovered.")
		return nil
	},
}

var keyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List virtual keys",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()
		store, closeDB, err := openKeyStore(ctx)
		if err != nil {
			return err
		}
		defer closeDB()

		keys, err := store.ListKeys(ctx)
		if err != nil {
			return err
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			type keyOut struct {
				ID          string   `json:"id" yaml:"id"`
				Name        string   `json:"name" yaml:"name"`
				Prefix      string   `json:"prefix" yaml:"prefix"`
				Models      []string `json:"models" yaml:"models"`
				TokenBudget int64    `json:"token_budget" yaml:"token_budget"`
				USDBudget   float64  `json:"usd_budget" yaml:"usd_budget"`
				Revoked     bool     `json:"revoked" yaml:"revoked"`
				CreatedAt   string   `json:"created_at" yaml:"created_at"`
			}
			out := make([]keyOut, len(keys))
			for i, k := range keys {
				out[i] = keyOut{
					ID: k.ID, Name: k.Name, Prefix: k.Prefix, Models: k.Models,
					TokenBudget: k.TokenBudget, USDBudget: k.USDBudget,
					Revoked: k.Revoked, CreatedAt: k.CreatedAt.Format(time.RFC3339),
				}
			}
			return output.NewWriter(cfg.Format).Print(out)
		}

		table := output.Table{
			Headers: []string{"ID", "NAME", "PREFIX", "MODELS", "TOKEN BUDGET", "USD BUDGET", "REVOKED"},
		}
		for _, k := range keys {
			models := strings.Join(k.Models, ",")
			if models == "" {
				models = "*"
			}
			tokenBudget := "unlimited"
			if k.TokenBudget > 0 {
				tokenBudget = fmt.Sprintf("%d", k.TokenBudget)
			}
			usdBudget := "unlimited"
			if k.USDBudget > 0 {
				usdBudget = fmt.Sprintf("$%.2f", k.USDBudget)
			}
			table.Rows = append(table.Rows, []string{
				k.ID, k.Name, k.Prefix + "…", models, tokenBudget, usdBudget, fmt.Sprintf("%v", k.Revoked),
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
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()
		store, closeDB, err := openKeyStore(ctx)
		if err != nil {
			return err
		}
		defer closeDB()

		if err := store.RevokeKey(ctx, args[0]); err != nil {
			return fmt.Errorf("failed to revoke key %s: %w", args[0], err)
		}
		cmd.Printf("Revoked key %s (takes effect within one minute on running gateways)\n", args[0])
		return nil
	},
}

func init() {
	keyCreateCmd.Flags().StringSlice("models", nil, "Models this key may use (default: all; supports provider/* wildcards)")
	keyCreateCmd.Flags().Int64("token-budget", 0, "Monthly token budget (0 = unlimited)")
	keyCreateCmd.Flags().Float64("usd-budget", 0, "Monthly dollar budget (0 = unlimited)")

	keyCmd.AddCommand(keyCreateCmd)
	keyCmd.AddCommand(keyListCmd)
	keyCmd.AddCommand(keyRevokeCmd)
	rootCmd.AddCommand(keyCmd)
}
