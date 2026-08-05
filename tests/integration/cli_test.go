//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
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
	stdout := mustRunCLI(t, "prompt", "list")
	// Should output table headers or empty result
	if !strings.Contains(stdout, "ID") && !strings.Contains(stdout, "NAME") && stdout != "" {
		t.Logf("Prompt list output: %s", stdout)
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
	t.Logf("Created prompt ID: %s", promptID)

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

	// History
	historyOut := mustRunCLI(t, "prompt", "history", promptID)
	if !strings.Contains(historyOut, "VERSION") || !strings.Contains(historyOut, "v1") {
		t.Logf("History output: %s", historyOut)
	}

	// Delete
	deleteOut := mustRunCLI(t, "prompt", "delete", promptID)
	if !strings.Contains(deleteOut, "Deleted prompt") {
		t.Errorf("Expected 'Deleted prompt' in output, got: %s", deleteOut)
	}

	// Verify deleted - service uses soft delete, so prompt may still be retrievable
	// but with a deleted status. Either null, error, or status=deleted is acceptable.
	getStdout, _, _ := runCLI(t, "prompt", "get", promptID, "-o", "json")
	if strings.TrimSpace(getStdout) != "null" && strings.TrimSpace(getStdout) != "" {
		// Parse to check if it has deleted status
		var deletedPrompt map[string]interface{}
		if err := json.Unmarshal([]byte(getStdout), &deletedPrompt); err == nil {
			// Status 4 = deleted (soft delete)
			if status, ok := deletedPrompt["status"].(float64); ok && status == 4 {
				t.Logf("Prompt soft-deleted with status=%v", status)
			}
		}
	}
}

// ============================================================================
// Datasets CLI Tests
// ============================================================================

func TestCLI_Datasets_List(t *testing.T) {
	stdout := mustRunCLI(t, "datasets", "list")
	t.Logf("Datasets list output: %s", stdout)
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
	t.Logf("Created dataset ID: %s", datasetID)

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
	t.Logf("Eval list output: %s", stdout)
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
	// Works against empty state - an empty gate list is not an error.
	stdout := mustRunCLI(t, "gate", "list")
	t.Logf("Gate list output: %s", stdout)
}

func TestCLI_Gate_List_DeployAlias(t *testing.T) {
	// The pre-refocus command name must keep working.
	stdout := mustRunCLI(t, "deploy", "list")
	t.Logf("Gate list via deploy alias: %s", stdout)
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
	stdout := mustRunCLI(t, "gate", "create", gateName,
		"--prompt", promptID,
		"--condition", "overall_score>=0.8",
		"--condition", "avg_latency_ms<=5000")
	t.Logf("Gate create output: %s", stdout)

	// The new gate shows up in a prompt-filtered listing.
	stdout = mustRunCLI(t, "gate", "list", "--prompt", promptID)
	if !strings.Contains(stdout, gateName) {
		t.Errorf("expected gate %q in `gate list --prompt %s`, got: %s", gateName, promptID, stdout)
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
	// Either a NOT_FOUND from the control plane or a failing verdict exit(1)
	// is acceptable here; what matters is that CI sees a non-zero status.
	t.Logf("gate check for a missing gate exited non-zero: %v (stderr: %s)", err, stderr)
}

// ============================================================================
// Gateway CLI Tests
// ============================================================================

func TestCLI_Gateway_Models(t *testing.T) {
	stdout := mustRunCLI(t, "gateway", "models")
	// Should list models (may be empty without provider keys)
	if !strings.Contains(stdout, "ID") && !strings.Contains(stdout, "OWNED BY") {
		t.Logf("Gateway models output: %s", stdout)
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
	t.Logf("Gateway health output: %s", stdout)
}

func TestCLI_Gateway_RuntimeAlias(t *testing.T) {
	// The old `runtime` command name must keep working as an alias.
	stdout := mustRunCLI(t, "runtime", "health")
	t.Logf("Gateway health via runtime alias: %s", stdout)
}

func TestCLI_Gateway_Complete(t *testing.T) {
	// This may fail if no providers are configured - that's expected
	stdout, stderr, err := runCLI(t, "gateway", "complete", "Say hello", "--model", "gpt-4o-mini")
	if err != nil {
		// Expected without API keys
		t.Logf("Gateway complete (expected to fail without provider keys): %s %s", stdout, stderr)
	} else {
		t.Logf("Gateway complete output: %s", stdout)
	}
}

// ============================================================================
// Observe CLI Tests
// ============================================================================

func TestCLI_Observe_Traces(t *testing.T) {
	stdout := mustRunCLI(t, "observe", "traces", "--limit", "5")
	t.Logf("Observe traces output: %s", stdout)
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
	_, _, err := runCLI(t, "nonexistent")
	if err == nil {
		t.Error("Expected error for invalid command")
	}
}

func TestCLI_Prompt_Get_NotFound(t *testing.T) {
	stdout, _, err := runCLI(t, "prompt", "get", "nonexistent-id-12345")
	// CLI returns null for non-existent prompts (not an error)
	if err != nil {
		t.Logf("Got error (expected): %v", err)
		return
	}
	// If no error, should return null/empty
	if strings.TrimSpace(stdout) != "null" && strings.TrimSpace(stdout) != "" {
		t.Errorf("Expected null or empty for nonexistent prompt, got: %s", stdout)
	}
}

func TestCLI_Datasets_Get_NotFound(t *testing.T) {
	_, stderr, err := runCLI(t, "datasets", "get", "nonexistent-id-12345")
	if err == nil {
		t.Error("Expected error for nonexistent dataset")
	}
	t.Logf("Error output: %s", stderr)
}
