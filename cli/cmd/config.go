package cmd

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	pkgconfig "github.com/instantcocoa/delos/pkg/config"
	"github.com/instantcocoa/delos/pkg/database"
)

// `delos config validate` answers "is this file well-formed?" offline.
// `delos config doctor` answers "does this configuration actually work?" by
// probing everything it points at. Both print plain language and exit 1 on
// failure, so CI can gate on them.

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Inspect and check Delos configuration",
	Long: `Work with delos.yaml.

Configuration precedence is: environment variable > delos.yaml > default.
The file is found via DELOS_CONFIG, or ./delos.yaml if it exists. Secrets
(provider API keys, DELOS_DB_PASSWORD) are read from the environment only and
never belong in the file.`,
}

var configValidateCmd = &cobra.Command{
	Use:   "validate [file]",
	Short: "Check a delos.yaml for errors",
	Long: `Parse a config file and report anything wrong with it in plain language.

With no argument the file is discovered the same way the servers discover it:
$DELOS_CONFIG, else ./delos.yaml. Exits 1 if the file is invalid.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		path := ""
		if len(args) == 1 {
			path = args[0]
		} else {
			path = pkgconfig.DiscoverFilePath()
		}
		if path == "" {
			cmd.Println("No config file found (no $DELOS_CONFIG, no ./delos.yaml).")
			cmd.Println("That is fine: Delos runs on defaults plus environment variables.")
			cmd.Println("Start from delos.yaml.example if you want one.")
			return nil
		}

		file, err := pkgconfig.LoadFile(path)
		if err != nil {
			cmd.PrintErrln("INVALID " + path)
			cmd.PrintErrln("  " + err.Error())
			return errSilentExit
		}
		problems := file.Validate()
		if len(problems) > 0 {
			cmd.PrintErrf("INVALID %s (%d problem(s))\n", path, len(problems))
			for _, p := range problems {
				cmd.PrintErrf("  %s\n", p.String())
			}
			cmd.PrintErrln("\nCompare against delos.yaml.example for the full documented schema.")
			return errSilentExit
		}
		cmd.Printf("OK %s\n", path)
		cmd.Println("Remember: provider keys and DELOS_DB_PASSWORD come from the environment, not this file.")
		return nil
	},
}

// errSilentExit makes the command exit 1 without cobra printing a redundant
// "Error:" line - the command has already said what is wrong, in full
// sentences.
var errSilentExit = errors.New("")

// check is one doctor probe result.
type check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Hint   string `json:"hint,omitempty"` // what to do when it failed
}

var configDoctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check that the configuration actually works",
	Long: `Validate the config file, then probe what it points at: the control
plane, the gateway, PostgreSQL (when configured), and provider connectivity
through the gateway.

Exits 1 if any check fails, so it can be used as a smoke test in CI.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()

		checks := runDoctor(ctx)

		if cfg != nil && cfg.Format == "json" {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			if err := enc.Encode(checks); err != nil {
				return err
			}
		} else {
			width := 0
			for _, c := range checks {
				if len(c.Name) > width {
					width = len(c.Name)
				}
			}
			for _, c := range checks {
				status := "ok  "
				if !c.OK {
					status = "FAIL"
				}
				cmd.Printf("%s  %-*s  %s\n", status, width, c.Name, c.Detail)
				if !c.OK && c.Hint != "" {
					cmd.Printf("      %-*s  -> %s\n", width, "", c.Hint)
				}
			}
		}

		failed := 0
		for _, c := range checks {
			if !c.OK {
				failed++
			}
		}
		if failed > 0 {
			cmd.PrintErrf("\n%d check(s) failed.\n", failed)
			return errSilentExit
		}
		cmd.Println("\nAll checks passed.")
		return nil
	},
}

// runDoctor executes every probe and returns the results in report order.
func runDoctor(ctx context.Context) []check {
	var checks []check

	// 1. Config file.
	path := pkgconfig.DiscoverFilePath()
	switch {
	case path == "":
		checks = append(checks, check{
			Name: "config file", OK: true,
			Detail: "none found; using defaults and environment variables",
		})
	default:
		file, err := pkgconfig.LoadFile(path)
		if err != nil {
			checks = append(checks, check{
				Name: "config file", Detail: err.Error(),
				Hint: "fix the syntax, or delete the file to fall back to defaults",
			})
		} else if problems := file.Validate(); len(problems) > 0 {
			msgs := make([]string, 0, len(problems))
			for _, p := range problems {
				msgs = append(msgs, p.String())
			}
			checks = append(checks, check{
				Name: "config file", Detail: path + ": " + strings.Join(msgs, "; "),
				Hint: "run `delos config validate` and compare with delos.yaml.example",
			})
		} else {
			checks = append(checks, check{Name: "config file", OK: true, Detail: path + " parses cleanly"})
		}
	}

	base, err := pkgconfig.Load("delos-cli")
	if err != nil {
		checks = append(checks, check{
			Name: "config load", Detail: err.Error(),
			Hint: "the servers will refuse to start with this configuration",
		})
		return checks
	}

	gatewayURL := base.GatewayURL
	if cfg != nil && cfg.GatewayURL != "" {
		gatewayURL = cfg.GatewayURL
	}
	cpAddr := "localhost:8081"
	if cfg != nil && cfg.ControlPlaneAddr != "" {
		cpAddr = cfg.ControlPlaneAddr
	}

	// 2. Control plane: TCP first (a clear "nothing listening"), then /healthz.
	checks = append(checks, checkControlPlane(ctx, cpAddr))

	// 3. Gateway health and provider count.
	gwCheck, providers := checkGateway(ctx, gatewayURL)
	checks = append(checks, gwCheck)

	// 4. Postgres, only when the configuration says it is used.
	if base.UsePostgresStorage() {
		checks = append(checks, checkPostgres(ctx, base))
	} else {
		checks = append(checks, check{
			Name: "postgres", OK: true,
			Detail: "not configured (storage: memory) - state is in-memory and lost on restart",
		})
	}

	// 5. Provider connectivity through the gateway.
	if gwCheck.OK {
		checks = append(checks, checkProviders(ctx, gatewayURL, providers))
	} else {
		checks = append(checks, check{
			Name: "providers", Detail: "skipped: the gateway is not reachable",
			Hint: "start the gateway first, then re-run `delos config doctor`",
		})
	}

	return checks
}

func checkControlPlane(ctx context.Context, addr string) check {
	dialer := net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return check{
			Name: "control plane", Detail: fmt.Sprintf("cannot connect to %s: %v", addr, err),
			Hint: "run `delos serve` (or `make run-control-plane`), or set DELOS_CONTROL_PLANE_ADDR",
		}
	}
	conn.Close()

	host := addr
	if h, _, splitErr := net.SplitHostPort(addr); splitErr == nil && h == "" {
		host = "localhost" + addr
	}
	healthURL := "http://" + host + "/healthz"
	body, status, err := httpGet(ctx, healthURL)
	if err != nil {
		return check{
			Name: "control plane", OK: true,
			Detail: fmt.Sprintf("%s accepts connections (health endpoint unreachable: %v)", addr, err),
		}
	}
	if status != http.StatusOK {
		return check{
			Name: "control plane", Detail: fmt.Sprintf("%s returned HTTP %d", healthURL, status),
			Hint: "check the control plane logs; a failing /healthz usually means the database is unreachable",
		}
	}
	return check{Name: "control plane", OK: true, Detail: fmt.Sprintf("%s healthy (%s)", addr, compact(body))}
}

// gatewayHealth is the gateway's /healthz payload.
type gatewayHealth struct {
	Status    string `json:"status"`
	Providers int    `json:"providers"`
}

func checkGateway(ctx context.Context, baseURL string) (check, int) {
	u := strings.TrimRight(baseURL, "/") + "/healthz"
	if _, err := url.Parse(u); err != nil {
		return check{Name: "gateway", Detail: fmt.Sprintf("%q is not a URL", baseURL),
			Hint: "set gateway.url in delos.yaml or DELOS_GATEWAY_URL"}, 0
	}
	body, status, err := httpGet(ctx, u)
	if err != nil {
		return check{
			Name: "gateway", Detail: fmt.Sprintf("cannot reach %s: %v", u, err),
			Hint: "start it with `delos-gateway` (or `make run-gateway`), or set DELOS_GATEWAY_URL",
		}, 0
	}
	if status != http.StatusOK {
		return check{Name: "gateway", Detail: fmt.Sprintf("%s returned HTTP %d", u, status),
			Hint: "check the gateway logs"}, 0
	}
	var health gatewayHealth
	_ = json.Unmarshal(body, &health)
	if health.Providers == 0 {
		return check{
			Name: "gateway", OK: true,
			Detail: fmt.Sprintf("%s healthy, but no providers are configured", baseURL),
		}, 0
	}
	return check{
		Name: "gateway", OK: true,
		Detail: fmt.Sprintf("%s healthy, %d provider(s) registered", baseURL, health.Providers),
	}, health.Providers
}

func checkPostgres(ctx context.Context, base *pkgconfig.Base) check {
	target := fmt.Sprintf("%s:%d/%s", base.DBHost, base.DBPort, base.DBName)
	db, err := database.Connect(ctx, &database.Config{
		Host:            base.DBHost,
		Port:            base.DBPort,
		User:            base.DBUser,
		Password:        base.DBPassword,
		Database:        base.DBName,
		SSLMode:         base.DBSSLMode,
		MaxOpenConns:    1,
		MaxIdleConns:    1,
		ConnMaxLifetime: time.Minute,
		ConnMaxIdleTime: time.Minute,
	})
	if err != nil {
		hint := "start it with `make up`, or fix DELOS_DB_* / database: in delos.yaml"
		if base.DBPassword == "" {
			hint += " (DELOS_DB_PASSWORD is empty)"
		}
		return check{Name: "postgres", Detail: fmt.Sprintf("cannot connect to %s: %v", target, err), Hint: hint}
	}
	defer db.Close()

	var version string
	if err := db.DB.QueryRowContext(ctx, "SELECT version()").Scan(&version); err != nil {
		if err == sql.ErrNoRows {
			return check{Name: "postgres", OK: true, Detail: "connected to " + target}
		}
		return check{Name: "postgres", Detail: fmt.Sprintf("connected to %s but queries fail: %v", target, err),
			Hint: "check the user's permissions on the database"}
	}
	return check{Name: "postgres", OK: true, Detail: fmt.Sprintf("connected to %s (%s)", target, firstWords(version, 2))}
}

func checkProviders(ctx context.Context, baseURL string, registered int) check {
	u := strings.TrimRight(baseURL, "/") + "/v1/models"
	body, status, err := httpGet(ctx, u)
	if err != nil {
		return check{Name: "providers", Detail: fmt.Sprintf("cannot reach %s: %v", u, err),
			Hint: "the gateway is up but not serving; check its logs"}
	}
	if status == http.StatusUnauthorized {
		return check{Name: "providers", Detail: "the gateway rejected the request (401)",
			Hint: "virtual keys are enforced; export DELOS_API_KEY with a key from `delos key create`"}
	}
	if status != http.StatusOK {
		return check{Name: "providers", Detail: fmt.Sprintf("%s returned HTTP %d: %s", u, status, compact(body)),
			Hint: "check provider credentials in the gateway's environment"}
	}
	var models modelsResponse
	if err := json.Unmarshal(body, &models); err != nil {
		return check{Name: "providers", Detail: "the model list was not valid JSON",
			Hint: "this is a gateway bug; please report it with the request id"}
	}
	if len(models.Data) == 0 {
		return check{
			Name:   "providers",
			Detail: fmt.Sprintf("%d provider(s) registered but no models are reachable", registered),
			Hint:   "check the API keys in the gateway's environment (OPENAI_API_KEY, ANTHROPIC_API_KEY, ...)",
		}
	}
	owners := map[string]bool{}
	for _, m := range models.Data {
		if m.OwnedBy != "" {
			owners[m.OwnedBy] = true
		}
	}
	names := make([]string, 0, len(owners))
	for o := range owners {
		names = append(names, o)
	}
	detail := fmt.Sprintf("%d model(s) reachable", len(models.Data))
	if len(names) > 0 {
		detail += " via " + strings.Join(names, ", ")
	}
	return check{Name: "providers", OK: true, Detail: detail}
}

// ---- small helpers ----

func httpGet(ctx context.Context, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	if key := os.Getenv("DELOS_API_KEY"); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for len(body) < 1<<20 {
		n, readErr := resp.Body.Read(buf)
		body = append(body, buf[:n]...)
		if readErr != nil {
			break
		}
	}
	return body, resp.StatusCode, nil
}

func compact(body []byte) string {
	s := strings.Join(strings.Fields(string(body)), " ")
	if len(s) > 120 {
		s = s[:117] + "..."
	}
	return s
}

func firstWords(s string, n int) string {
	fields := strings.Fields(s)
	if len(fields) > n {
		fields = fields[:n]
	}
	return strings.Join(fields, " ")
}

func init() {
	configCmd.AddCommand(configValidateCmd)
	configCmd.AddCommand(configDoctorCmd)
	rootCmd.AddCommand(configCmd)
}
