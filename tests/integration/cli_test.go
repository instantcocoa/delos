//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	cliBinary     string
	cliBinaryOnce sync.Once
	cliBuildErr   error
)

// ensureCLIBinary builds the CLI binary once for all tests
func ensureCLIBinary(t *testing.T) string {
	t.Helper()
	cliBinaryOnce.Do(func() {
		// An explicitly supplied binary wins, so CI can test exactly the
		// artifact it built.
		if b := os.Getenv("DELOS_CLI_BINARY"); b != "" {
			if _, err := os.Stat(b); err != nil {
				cliBuildErr = err
				return
			}
			cliBinary = b
			return
		}

		projectRoot := filepath.Join("..", "..")

		// Look for existing binary in bin/ first
		existingBinary := filepath.Join(projectRoot, "bin", "delos")
		if _, err := os.Stat(existingBinary); err == nil {
			cliBinary = existingBinary
			return
		}

		// Also check project root
		existingBinary = filepath.Join(projectRoot, "delos")
		if _, err := os.Stat(existingBinary); err == nil {
			cliBinary = existingBinary
			return
		}

		// Build to temp directory
		tmpDir, err := os.MkdirTemp("", "delos-cli-test")
		if err != nil {
			cliBuildErr = err
			return
		}

		cliBinary = filepath.Join(tmpDir, "delos")
		cmd := exec.Command("go", "build", "-o", cliBinary, filepath.Join(projectRoot, "cmd", "delos"))
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			cliBuildErr = err
			return
		}
	})

	if cliBuildErr != nil {
		t.Fatalf("Failed to build CLI: %v", cliBuildErr)
	}
	return cliBinary
}

// runCLI executes the CLI with given arguments and returns stdout, stderr, and error
func runCLI(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	ensureCLIBinary(t)
	cmd := exec.Command(cliBinary, args...)

	// Set environment for the two endpoints
	cmd.Env = append(os.Environ(),
		"DELOS_CONTROL_PLANE_ADDR="+getEnv("DELOS_CONTROL_PLANE_ADDR", "localhost:8081"),
		"DELOS_GATEWAY_URL="+getEnv("DELOS_GATEWAY_URL", "http://localhost:8080"),
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// mustRunCLI runs CLI and fails test on error
func mustRunCLI(t *testing.T, args ...string) string {
	t.Helper()
	stdout, stderr, err := runCLI(t, args...)
	if err != nil {
		t.Fatalf("CLI failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	return stdout
}

// exitCode returns the process exit status behind a runCLI error. It reports
// -1 when the command failed for a reason other than a non-zero exit (the
// binary could not be started, for example), which no test should accept as a
// stand-in for a clean failure.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// requireTableHeaders fails unless every header appears in the command output.
// An empty result still prints its header row, so this holds against empty state.
func requireTableHeaders(t *testing.T, what, output string, headers ...string) {
	t.Helper()
	for _, h := range headers {
		if !strings.Contains(output, h) {
			t.Errorf("%s: expected the %q column in the table output, got:\n%s", what, h, output)
		}
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ============================================================================
// CLI Basic Tests
// ============================================================================

func TestCLI_Version(t *testing.T) {
	stdout, stderr, err := runCLI(t, "version")
	if err != nil {
		t.Fatalf("version command failed: %v", err)
	}
	// Version may be in stdout or stderr
	output := stdout + stderr
	if !strings.Contains(output, "delos version") {
		t.Errorf("Expected version output, got stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestCLI_Help(t *testing.T) {
	stdout := mustRunCLI(t, "--help")
	// Check for key elements in help output
	if !strings.Contains(stdout, "Delos") {
		t.Errorf("Expected 'Delos' in help output, got: %s", stdout)
	}
	// Check that all subcommands are listed
	for _, cmd := range []string{"prompt", "datasets", "eval", "gate", "gateway", "observe", "serve"} {
		if !strings.Contains(stdout, cmd) {
			t.Errorf("Expected %q in help output", cmd)
		}
	}
}

// ============================================================================
// Prompt CLI Tests
// ============================================================================

func TestCLI_Prompt_List(t *testing.T) {
	slug := fmt.Sprintf("cli-list-prompt-%d", time.Now().UnixNano())
	mustRunCLI(t, "prompt", "create", "CLI List Prompt", "--slug", slug, "--system", "Test prompt")
	defer runCLI(t, "prompt", "delete", slug)

	stdout := mustRunCLI(t, "prompt", "list")
	requireTableHeaders(t, "prompt list", stdout, "ID", "NAME", "SLUG", "VERSION", "UPDATED")
	if !strings.Contains(stdout, slug) {
		t.Errorf("prompt list omitted the prompt just created (%s), got:\n%s", slug, stdout)
	}
}

func TestCLI_Prompt_List_JSON(t *testing.T) {
	stdout := mustRunCLI(t, "prompt", "list", "-o", "json")
	// Should be valid JSON (array)
	var result []interface{}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		// Empty result is also valid
		if stdout != "null\n" && stdout != "[]\n" {
			t.Errorf("Expected valid JSON array, got: %s", stdout)
		}
	}
}

func TestCLI_Prompt_CRUD(t *testing.T) {
	// Use unique slug with timestamp to avoid conflicts
	slug := fmt.Sprintf("cli-test-prompt-%d", time.Now().UnixNano())

	// Create
	stdout := mustRunCLI(t, "prompt", "create", "CLI Test Prompt",
		"--slug", slug,
		"--system", "You are a test assistant",
		"--description", "Created by CLI integration test")

	if !strings.Contains(stdout, "Created prompt") {
		t.Fatalf("Expected 'Created prompt' in output, got: %s", stdout)
	}

	// Extract ID from output or list and find it
	listOut := mustRunCLI(t, "prompt", "list", "-o", "json")
	var prompts []map[string]interface{}
	if err := json.Unmarshal([]byte(listOut), &prompts); err != nil {
		t.Fatalf("Failed to parse prompt list: %v", err)
	}

	var promptID string
	for _, p := range prompts {
		if s, ok := p["slug"].(string); ok && s == slug {
			promptID = p["id"].(string)
			break
		}
	}
	if promptID == "" {
		t.Fatal("Could not find created prompt")
	}

	// Get
	getOut := mustRunCLI(t, "prompt", "get", promptID, "-o", "json")
	var prompt map[string]interface{}
	if err := json.Unmarshal([]byte(getOut), &prompt); err != nil {
		t.Fatalf("Failed to parse prompt get: %v", err)
	}
	if prompt["slug"] != slug {
		t.Errorf("Expected slug '%s', got: %v", slug, prompt["slug"])
	}

	// Update
	updateOut := mustRunCLI(t, "prompt", "update", promptID,
		"--system", "Updated system prompt",
		"--change-description", "CLI test update")
	if !strings.Contains(updateOut, "Updated prompt") {
		t.Errorf("Expected 'Updated prompt' in output, got: %s", updateOut)
	}

	// History: after one update the prompt has two versions, and both must be
	// listed with the change description the update was saved under.
	historyOut := mustRunCLI(t, "prompt", "history", promptID)
	requireTableHeaders(t, "prompt history", historyOut,
		"VERSION", "UPDATED BY", "UPDATED AT", "CHANGE DESCRIPTION")
	for _, want := range []string{"v1", "v2"} {
		if !strings.Contains(historyOut, want) {
			t.Errorf("prompt history omitted %s, got:\n%s", want, historyOut)
		}
	}
	if !strings.Contains(historyOut, "CLI test update") {
		t.Errorf("prompt history omitted the change description of the update, got:\n%s", historyOut)
	}

	// Delete
	deleteOut := mustRunCLI(t, "prompt", "delete", promptID)
	if !strings.Contains(deleteOut, "Deleted prompt") {
		t.Errorf("Expected 'Deleted prompt' in output, got: %s", deleteOut)
	}

	// Delete is a soft delete, so the prompt is still retrievable - but it must
	// no longer show up in the default listing.
	listAfter := mustRunCLI(t, "prompt", "list")
	if strings.Contains(listAfter, slug) {
		t.Errorf("a deleted prompt is still listed by `prompt list`: %s\n%s", slug, listAfter)
	}
}

// ============================================================================
// Datasets CLI Tests
// ============================================================================

func TestCLI_Datasets_List(t *testing.T) {
	stdout := mustRunCLI(t, "datasets", "list")
	requireTableHeaders(t, "datasets list", stdout, "ID", "NAME", "PROMPT", "EXAMPLES", "UPDATED")
}

func TestCLI_Datasets_List_JSON(t *testing.T) {
	stdout := mustRunCLI(t, "datasets", "list", "-o", "json")
	var result []interface{}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		if stdout != "null\n" && stdout != "[]\n" {
			t.Errorf("Expected valid JSON array, got: %s", stdout)
		}
	}
}

func TestCLI_Datasets_CRUD(t *testing.T) {
	// Create
	stdout := mustRunCLI(t, "datasets", "create", "CLI Test Dataset",
		"--description", "Created by CLI integration test",
		"--tags", "cli-test")

	if !strings.Contains(stdout, "Created dataset") {
		t.Fatalf("Expected 'Created dataset' in output, got: %s", stdout)
	}

	// Find dataset ID
	listOut := mustRunCLI(t, "datasets", "list", "-o", "json")
	var datasets []map[string]interface{}
	if err := json.Unmarshal([]byte(listOut), &datasets); err != nil {
		t.Fatalf("Failed to parse datasets list: %v", err)
	}

	var datasetID string
	for _, d := range datasets {
		if name, ok := d["name"].(string); ok && name == "CLI Test Dataset" {
			datasetID = d["id"].(string)
			break
		}
	}
	if datasetID == "" {
		t.Fatal("Could not find created dataset")
	}

	// Get
	getOut := mustRunCLI(t, "datasets", "get", datasetID, "-o", "json")
	var dataset map[string]interface{}
	if err := json.Unmarshal([]byte(getOut), &dataset); err != nil {
		t.Fatalf("Failed to parse dataset get: %v", err)
	}
	if dataset["name"] != "CLI Test Dataset" {
		t.Errorf("Expected name 'CLI Test Dataset', got: %v", dataset["name"])
	}

	// Delete
	deleteOut := mustRunCLI(t, "datasets", "delete", datasetID)
	if !strings.Contains(deleteOut, "Deleted dataset") {
		t.Errorf("Expected 'Deleted dataset' in output, got: %s", deleteOut)
	}
}

// ============================================================================
// Eval CLI Tests
// ============================================================================

func TestCLI_Eval_List(t *testing.T) {
	stdout := mustRunCLI(t, "eval", "list")
	requireTableHeaders(t, "eval list", stdout, "ID", "NAME", "STATUS", "PROGRESS", "SCORE", "CREATED")
}

func TestCLI_Eval_Evaluators(t *testing.T) {
	stdout := mustRunCLI(t, "eval", "evaluators")
	// Should list available evaluators
	if !strings.Contains(stdout, "exact_match") && !strings.Contains(stdout, "TYPE") {
		t.Errorf("Expected evaluators list, got: %s", stdout)
	}
}

func TestCLI_Eval_Evaluators_JSON(t *testing.T) {
	stdout := mustRunCLI(t, "eval", "evaluators", "-o", "json")
	var evaluators []map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &evaluators); err != nil {
		t.Fatalf("Failed to parse evaluators JSON: %v", err)
	}
	if len(evaluators) == 0 {
		t.Error("Expected at least one evaluator")
	}
	// Check for expected evaluator types
	types := make(map[string]bool)
	for _, e := range evaluators {
		if t, ok := e["type"].(string); ok {
			types[t] = true
		}
	}
	for _, expected := range []string{"exact_match", "contains", "semantic_similarity"} {
		if !types[expected] {
			t.Errorf("Expected evaluator type %q", expected)
		}
	}
}

// ============================================================================
// Quality Gate CLI Tests
// ============================================================================
//
// `delos gate` replaced `delos deploy`; the old name survives as an alias.
// The CI contract is `delos gate check <name>`: exit 0 on pass, non-zero
// otherwise.

func TestCLI_Gate_List(t *testing.T) {
	// Works against empty state - an empty gate list is not an error, but it
	// still prints its columns.
	stdout := mustRunCLI(t, "gate", "list")
	requireTableHeaders(t, "gate list", stdout, "NAME", "PROMPT", "CONDITIONS", "DESCRIPTION")
}

// TestCLI_Gate_List_DeployAlias pins the pre-refocus command name: `deploy list`
// must still produce what `gate list` produces, not merely exit 0.
func TestCLI_Gate_List_DeployAlias(t *testing.T) {
	canonical := mustRunCLI(t, "gate", "list")
	alias := mustRunCLI(t, "deploy", "list")

	requireTableHeaders(t, "deploy list", alias, "NAME", "PROMPT", "CONDITIONS", "DESCRIPTION")
	if alias != canonical {
		t.Errorf("`deploy list` and `gate list` disagree:\ngate:\n%s\ndeploy:\n%s",
			canonical, alias)
	}
}

func TestCLI_Gate_Create_Validation(t *testing.T) {
	// These fail on argument validation before any control-plane call, so
	// they hold regardless of server state.
	tests := []struct {
		name     string
		args     []string
		contains string
	}{
		{
			name:     "missing name",
			args:     []string{"gate", "create"},
			contains: "arg",
		},
		{
			name:     "missing prompt",
			args:     []string{"gate", "create", "some-gate", "--condition", "overall_score>=0.8"},
			contains: "--prompt",
		},
		{
			name:     "missing condition",
			args:     []string{"gate", "create", "some-gate", "--prompt", "some-prompt"},
			contains: "--condition",
		},
		{
			name:     "malformed condition",
			args:     []string{"gate", "create", "some-gate", "--prompt", "some-prompt", "--condition", "overall_score"},
			contains: "condition",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := runCLI(t, tt.args...)
			if err == nil {
				t.Fatalf("expected a validation error, got stdout: %s", stdout)
			}
			output := strings.ToLower(stdout + stderr)
			if !strings.Contains(output, strings.ToLower(tt.contains)) {
				t.Errorf("expected the error to mention %q, got: %s", tt.contains, output)
			}
		})
	}
}

func TestCLI_Gate_Create_And_List(t *testing.T) {
	timestamp := time.Now().UnixNano()
	slug := fmt.Sprintf("gate-cli-test-%d", timestamp)

	mustRunCLI(t, "prompt", "create", "Gate CLI Test",
		"--slug", slug,
		"--system", "Test prompt")

	listOut := mustRunCLI(t, "prompt", "list", "-o", "json")
	var prompts []map[string]interface{}
	json.Unmarshal([]byte(listOut), &prompts)

	var promptID string
	for _, p := range prompts {
		if s, ok := p["slug"].(string); ok && s == slug {
			promptID, _ = p["id"].(string)
			break
		}
	}
	if promptID == "" {
		t.Skip("could not resolve the created prompt id - skipping")
	}
	defer runCLI(t, "prompt", "delete", promptID)

	gateName := fmt.Sprintf("cli-gate-%d", timestamp)
	mustRunCLI(t, "gate", "create", gateName,
		"--prompt", promptID,
		"--condition", "overall_score>=0.8",
		"--condition", "avg_latency_ms<=5000")

	// The new gate shows up in a prompt-filtered listing, with the conditions
	// it was created with.
	stdout := mustRunCLI(t, "gate", "list", "--prompt", promptID)
	if !strings.Contains(stdout, gateName) {
		t.Errorf("expected gate %q in `gate list --prompt %s`, got: %s", gateName, promptID, stdout)
	}
	for _, want := range []string{"overall_score", "avg_latency_ms"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("expected condition %q in the gate listing, got: %s", want, stdout)
		}
	}
}

// TestCLI_Gate_Check_NotFound pins the CI contract: an unknown gate is a
// non-zero exit, never a silent success.
func TestCLI_Gate_Check_NotFound(t *testing.T) {
	name := fmt.Sprintf("no-such-gate-%d", time.Now().UnixNano())
	stdout, stderr, err := runCLI(t, "gate", "check", name)
	if err == nil {
		t.Fatalf("expected a non-zero exit for a missing gate, got stdout: %s", stdout)
	}
	// Exit 1 specifically: a crash or a signal death also produces "an error",
	// and CI must not read those as a clean gate failure.
	if code := exitCode(err); code != 1 {
		t.Errorf("expected exit status 1 for a missing gate, got %d (%v)", code, err)
	}
	// The operator has to be able to tell which gate CI was looking for.
	if !strings.Contains(stdout+stderr, name) {
		t.Errorf("expected the failure to name the gate %q, got stdout:%s stderr:%s",
			name, stdout, stderr)
	}
	if !strings.Contains(strings.ToLower(stdout+stderr), "not found") {
		t.Errorf("expected the failure to say the gate was not found, got stdout:%s stderr:%s",
			stdout, stderr)
	}
}

// ============================================================================
// Gateway CLI Tests
// ============================================================================

func TestCLI_Gateway_Models(t *testing.T) {
	stdout := mustRunCLI(t, "gateway", "models")
	// The columns print even when no provider is configured.
	requireTableHeaders(t, "gateway models", stdout, "ID", "OWNED BY")

	// Whatever the CLI shows must match what the gateway serves.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, m := range listGatewayModels(t, ctx).Data {
		if !strings.Contains(stdout, m.ID) {
			t.Errorf("`gateway models` omitted %q, which GET /v1/models advertises:\n%s",
				m.ID, stdout)
		}
	}
}

func TestCLI_Gateway_Models_JSON(t *testing.T) {
	stdout := mustRunCLI(t, "gateway", "models", "-o", "json")
	var models []map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &models); err != nil {
		// May be empty if no providers configured
		if stdout != "null\n" && stdout != "[]\n" {
			t.Errorf("Expected valid JSON, got: %s", stdout)
		}
	}
}

func TestCLI_Gateway_Health(t *testing.T) {
	stdout := mustRunCLI(t, "gateway", "health")

	// The command reports the gateway it reached, its status, and how many
	// providers are configured.
	if !strings.Contains(stdout, getEnv("DELOS_GATEWAY_URL", "http://localhost:8080")) {
		t.Errorf("expected the health output to name the gateway URL it probed, got: %s", stdout)
	}
	if strings.Contains(stdout, "unknown") {
		t.Errorf("gateway health could not read a status from /healthz: %s", stdout)
	}
	if !strings.Contains(stdout, "Providers configured:") {
		t.Errorf("expected the provider count in the health output, got: %s", stdout)
	}
}

// TestCLI_Gateway_RuntimeAlias pins the pre-refocus command name: `runtime
// health` must do what `gateway health` does, not merely exit 0.
func TestCLI_Gateway_RuntimeAlias(t *testing.T) {
	canonical := mustRunCLI(t, "gateway", "health")
	alias := mustRunCLI(t, "runtime", "health")
	if alias != canonical {
		t.Errorf("`runtime health` and `gateway health` disagree:\ngateway:\n%s\nruntime:\n%s",
			canonical, alias)
	}
}

func TestCLI_Gateway_Complete(t *testing.T) {
	// Skips visibly when no provider is configured; otherwise the completion
	// must actually succeed against a model the gateway really serves.
	model := requireGatewayModel(t)

	stdout, stderr, err := runCLI(t, "gateway", "complete", "Say hello", "--model", model)
	if err != nil {
		t.Fatalf("`gateway complete --model %s` failed with exit %d: stdout:%s stderr:%s",
			model, exitCode(err), stdout, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Errorf("expected the completion to print the model's reply, got empty stdout (stderr: %s)",
			stderr)
	}
}

// TestCLI_Gateway_Complete_UnknownModel pins the failure path: an unroutable
// model is a non-zero exit that names the model.
func TestCLI_Gateway_Complete_UnknownModel(t *testing.T) {
	model := fmt.Sprintf("no-such-provider/no-such-model-%d", time.Now().UnixNano())
	stdout, stderr, err := runCLI(t, "gateway", "complete", "Say hello", "--model", model)
	if err == nil {
		t.Fatalf("expected a non-zero exit for an unknown model, got stdout: %s", stdout)
	}
	if code := exitCode(err); code != 1 {
		t.Errorf("expected exit status 1 for an unknown model, got %d (%v)", code, err)
	}
	if !strings.Contains(stdout+stderr, model) {
		t.Errorf("expected the error to name the model %q, got stdout:%s stderr:%s",
			model, stdout, stderr)
	}
}

// ============================================================================
// Observe CLI Tests
// ============================================================================

func TestCLI_Observe_Traces(t *testing.T) {
	stdout := mustRunCLI(t, "observe", "traces", "--limit", "5")
	requireTableHeaders(t, "observe traces", stdout,
		"TRACE ID", "SERVICE", "OPERATION", "DURATION", "TIME")
}

func TestCLI_Observe_Traces_JSON(t *testing.T) {
	stdout := mustRunCLI(t, "observe", "traces", "--limit", "5", "-o", "json")
	var traces []interface{}
	if err := json.Unmarshal([]byte(stdout), &traces); err != nil {
		if stdout != "null\n" && stdout != "[]\n" {
			t.Errorf("Expected valid JSON, got: %s", stdout)
		}
	}
}

// ============================================================================
// Output Format Tests
// ============================================================================

func TestCLI_OutputFormats(t *testing.T) {
	tests := []struct {
		format   string
		validate func(t *testing.T, output string)
	}{
		{
			format: "json",
			validate: func(t *testing.T, output string) {
				var result interface{}
				if err := json.Unmarshal([]byte(output), &result); err != nil {
					if output != "null\n" && output != "[]\n" {
						t.Errorf("Invalid JSON: %v", err)
					}
				}
			},
		},
		{
			format: "yaml",
			validate: func(t *testing.T, output string) {
				// YAML should not start with '{'
				if strings.HasPrefix(strings.TrimSpace(output), "{") {
					t.Error("Expected YAML format, got JSON-like output")
				}
			},
		},
		{
			format: "table",
			validate: func(t *testing.T, output string) {
				// Table output typically has headers
				// Just check it's not JSON
				if strings.HasPrefix(strings.TrimSpace(output), "[") {
					t.Error("Expected table format, got JSON-like output")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			stdout := mustRunCLI(t, "prompt", "list", "-o", tt.format)
			tt.validate(t, stdout)
		})
	}
}

// ============================================================================
// Error Handling Tests
// ============================================================================

func TestCLI_InvalidCommand(t *testing.T) {
	stdout, stderr, err := runCLI(t, "nonexistent")
	if err == nil {
		t.Fatalf("Expected an error for an invalid command, got stdout: %s", stdout)
	}
	if code := exitCode(err); code != 1 {
		t.Errorf("expected exit status 1 for an unknown command, got %d (%v)", code, err)
	}
	if !strings.Contains(stdout+stderr, "nonexistent") {
		t.Errorf("expected the error to name the unknown command, got stdout:%s stderr:%s",
			stdout, stderr)
	}
}

// TestCLI_Prompt_Get_NotFound pins the scripting contract: asking for a prompt
// that does not exist is a non-zero exit naming it, not a successful "null".
func TestCLI_Prompt_Get_NotFound(t *testing.T) {
	ref := fmt.Sprintf("nonexistent-prompt-%d", time.Now().UnixNano())
	stdout, stderr, err := runCLI(t, "prompt", "get", ref)
	if err == nil {
		t.Fatalf("expected a non-zero exit for a nonexistent prompt, got stdout: %s", stdout)
	}
	if code := exitCode(err); code != 1 {
		t.Errorf("expected exit status 1, got %d (%v)", code, err)
	}
	if !strings.Contains(strings.ToLower(stdout+stderr), "not found") {
		t.Errorf("expected the error to say the prompt was not found, got stdout:%s stderr:%s",
			stdout, stderr)
	}
	if !strings.Contains(stdout+stderr, ref) {
		t.Errorf("expected the error to name %q, got stdout:%s stderr:%s", ref, stdout, stderr)
	}
}

func TestCLI_Datasets_Get_NotFound(t *testing.T) {
	id := fmt.Sprintf("nonexistent-dataset-%d", time.Now().UnixNano())
	stdout, stderr, err := runCLI(t, "datasets", "get", id)
	if err == nil {
		t.Fatalf("expected a non-zero exit for a nonexistent dataset, got stdout: %s", stdout)
	}
	if code := exitCode(err); code != 1 {
		t.Errorf("expected exit status 1, got %d (%v)", code, err)
	}
	if !strings.Contains(strings.ToLower(stdout+stderr), "not found") {
		t.Errorf("expected the error to say the dataset was not found, got stdout:%s stderr:%s",
			stdout, stderr)
	}
}
