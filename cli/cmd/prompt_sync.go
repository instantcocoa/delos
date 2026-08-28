package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	cpclient "github.com/instantcocoa/delos/cli/internal/client"
	"github.com/instantcocoa/delos/cli/internal/output"
	"github.com/instantcocoa/delos/cli/internal/promptfile"
	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
)

// Git-native prompt workflow: prompts live as *.prompt.yaml files in the
// repo (the source of truth); the control plane is an index and serving
// cache. pull/push/diff sync a directory, render works entirely locally.

const defaultPromptDir = "prompts"

func promptDirArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return defaultPromptDir
}

func promptClientConn() (promptv1.PromptServiceClient, *grpc.ClientConn, error) {
	conn, err := cpclient.Dial(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to control plane at %s: %w", cfg.ControlPlaneAddr, err)
	}
	return promptv1.NewPromptServiceClient(conn), conn, nil
}

// listRemotePrompts fetches every prompt, keyed by slug.
func listRemotePrompts(ctx context.Context, client promptv1.PromptServiceClient) (map[string]*promptv1.Prompt, error) {
	remote := map[string]*promptv1.Prompt{}
	offset := int32(0)
	for {
		resp, err := client.ListPrompts(ctx, &promptv1.ListPromptsRequest{Limit: 200, Offset: offset})
		if err != nil {
			return nil, fmt.Errorf("failed to list prompts: %w", err)
		}
		for _, p := range resp.Prompts {
			remote[p.Slug] = p
		}
		if len(resp.Prompts) < 200 {
			return remote, nil
		}
		offset += int32(len(resp.Prompts))
	}
}

var promptPullCmd = &cobra.Command{
	Use:   "pull [dir]",
	Short: "Write every prompt from the control plane into a directory of .prompt.yaml files",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := promptDirArg(args)
		client, conn, err := promptClientConn()
		if err != nil {
			return err
		}
		defer conn.Close()

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()
		remote, err := listRemotePrompts(ctx, client)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}

		created, updated, unchanged := 0, 0, 0
		for slug, p := range remote {
			pf := promptfile.FromProto(p)
			path := promptfile.PathFor(dir, slug)
			existing, err := promptfile.Load(path)
			switch {
			case err != nil: // no local file (or unreadable): write it
				if err := promptfile.Save(path, pf); err != nil {
					return fmt.Errorf("failed to write %s: %w", path, err)
				}
				created++
			case len(promptfile.SemanticDiff(existing, pf)) == 0:
				unchanged++
			default:
				if err := promptfile.Save(path, pf); err != nil {
					return fmt.Errorf("failed to write %s: %w", path, err)
				}
				updated++
			}
		}
		output.Success(fmt.Sprintf("Pulled %d prompt(s) into %s: %d new, %d updated, %d unchanged",
			len(remote), dir, created, updated, unchanged))
		return nil
	},
}

var promptPushCmd = &cobra.Command{
	Use:   "push [dir]",
	Short: "Sync local .prompt.yaml files to the control plane (repo is the source of truth)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := promptDirArg(args)
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		paths, err := promptfile.Discover(dir)
		if err != nil {
			return err
		}
		if len(paths) == 0 {
			return fmt.Errorf("no *.prompt.yaml files found in %s", dir)
		}

		client, conn, err := promptClientConn()
		if err != nil {
			return err
		}
		defer conn.Close()

		ctx, cancel := context.WithTimeout(cmd.Context(), 120*time.Second)
		defer cancel()
		remote, err := listRemotePrompts(ctx, client)
		if err != nil {
			return err
		}

		created, updated, unchanged := 0, 0, 0
		for _, path := range paths {
			local, err := promptfile.Load(path)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}

			existing, known := remote[local.Slug]
			switch {
			case !known:
				if dryRun {
					output.Info("would create %s (%s)", local.Slug, path)
				} else {
					if _, err := client.CreatePrompt(ctx, local.ToCreateRequest()); err != nil {
						return fmt.Errorf("failed to create %s: %w", local.Slug, err)
					}
					output.Info("created %s", local.Slug)
				}
				created++
			case len(promptfile.SemanticDiff(promptfile.FromProto(existing), local)) == 0:
				unchanged++
			default:
				if dryRun {
					output.Info("would update %s (v%d -> v%d)", local.Slug, existing.Version, existing.Version+1)
				} else {
					req := local.ToUpdateRequest(existing.Id, "pushed from "+path)
					if _, err := client.UpdatePrompt(ctx, req); err != nil {
						return fmt.Errorf("failed to update %s: %w", local.Slug, err)
					}
					output.Info("updated %s -> v%d", local.Slug, existing.Version+1)
				}
				updated++
			}
		}

		verb := "Pushed"
		if dryRun {
			verb = "Would push"
		}
		output.Success(fmt.Sprintf("%s %d prompt(s): %d created, %d updated, %d unchanged",
			verb, len(paths), created, updated, unchanged))
		return nil
	},
}

var promptDiffCmd = &cobra.Command{
	Use:   "diff [dir]",
	Short: "Show semantic differences between local prompt files and the control plane",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := promptDirArg(args)
		paths, err := promptfile.Discover(dir)
		if err != nil {
			return err
		}

		client, conn, err := promptClientConn()
		if err != nil {
			return err
		}
		defer conn.Close()

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()
		remote, err := listRemotePrompts(ctx, client)
		if err != nil {
			return err
		}

		clean := true
		seen := map[string]bool{}
		for _, path := range paths {
			local, err := promptfile.Load(path)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			seen[local.Slug] = true

			existing, known := remote[local.Slug]
			if !known {
				clean = false
				output.Info("%s: only local (would be created on push)", local.Slug)
				continue
			}
			changes := promptfile.SemanticDiff(promptfile.FromProto(existing), local)
			if len(changes) == 0 {
				continue
			}
			clean = false
			output.Info("%s (remote v%d):", local.Slug, existing.Version)
			for _, c := range changes {
				fmt.Fprintf(cmd.OutOrStdout(), "    %s\n", c)
			}
		}
		for slug := range remote {
			if !seen[slug] {
				clean = false
				output.Info("%s: only remote (pull to fetch)", slug)
			}
		}
		if clean {
			output.Success("Local prompt files match the control plane.")
		}
		return nil
	},
}

var promptRenderCmd = &cobra.Command{
	Use:   "render <file>",
	Short: "Render a prompt file with variables (fully local, no server call)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vars, _ := cmd.Flags().GetStringToString("var")

		pf, err := promptfile.Load(args[0])
		if err != nil {
			return err
		}
		messages, err := promptfile.Render(pf, vars)
		if err != nil {
			return err
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			return output.NewWriter(cfg.Format).Print(messages)
		}
		for _, m := range messages {
			fmt.Fprintf(cmd.OutOrStdout(), "[%s]\n%s\n\n", m.Role, m.Content)
		}
		return nil
	},
}

func init() {
	promptPushCmd.Flags().Bool("dry-run", false, "Show what would change without writing")
	promptRenderCmd.Flags().StringToString("var", nil, "Variable value, repeatable (--var name=value)")

	promptCmd.AddCommand(promptPullCmd)
	promptCmd.AddCommand(promptPushCmd)
	promptCmd.AddCommand(promptDiffCmd)
	promptCmd.AddCommand(promptRenderCmd)
}
