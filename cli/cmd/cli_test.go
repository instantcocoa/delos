package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/instantcocoa/delos/cli/internal/config"
)

// TestShortID covers the panic that `id[:8]` caused for IDs shorter than the
// slice bound.
func TestShortID(t *testing.T) {
	tests := []struct {
		id   string
		n    int
		want string
	}{
		{id: "", n: 8, want: ""},
		{id: "abc", n: 8, want: "abc"},
		{id: "abcdefgh", n: 8, want: "abcdefgh"},
		{id: "abcdefghij", n: 8, want: "abcdefgh"},
		{id: "abcdefghij", n: 0, want: "abcdefghij"},
		{id: "abcdefghij", n: -1, want: "abcdefghij"},
	}
	for _, tc := range tests {
		if got := shortID(tc.id, tc.n); got != tc.want {
			t.Errorf("shortID(%q, %d) = %q, want %q", tc.id, tc.n, got, tc.want)
		}
	}
}

func TestParseVersionArg(t *testing.T) {
	tests := []struct {
		in      string
		want    int32
		wantErr bool
	}{
		{in: "1", want: 1},
		{in: "2", want: 2},
		{in: "v3", want: 3},
		{in: "abc", wantErr: true},
		{in: "", wantErr: true},
		{in: "0", wantErr: true},
		{in: "-1", wantErr: true},
	}
	for _, tc := range tests {
		got, err := parseVersionArg(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseVersionArg(%q) = %d, want an error (a silent 0 compared v0 to v0)", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseVersionArg(%q) error = %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("parseVersionArg(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseExampleFlag(t *testing.T) {
	ex, err := parseExampleFlag("hello=>Hello!")
	if err != nil {
		t.Fatalf("parseExampleFlag() error = %v", err)
	}
	if got := ex.Input.AsMap()["input"]; got != "hello" {
		t.Errorf("input = %v, want %q", got, "hello")
	}
	if got := ex.ExpectedOutput.AsMap()["output"]; got != "Hello!" {
		t.Errorf("expected output = %v, want %q", got, "Hello!")
	}

	ex, err = parseExampleFlag(`{"text":"hi"}=>{"answer":"Hello!"}`)
	if err != nil {
		t.Fatalf("parseExampleFlag(json) error = %v", err)
	}
	if got := ex.Input.AsMap()["text"]; got != "hi" {
		t.Errorf("input.text = %v, want %q", got, "hi")
	}
	if got := ex.ExpectedOutput.AsMap()["answer"]; got != "Hello!" {
		t.Errorf("expected.answer = %v, want %q", got, "Hello!")
	}

	for _, bad := range []string{"no separator", "=>only-expected", "only-input=>", `{broken=>x`} {
		if _, err := parseExampleFlag(bad); err == nil {
			t.Errorf("parseExampleFlag(%q) succeeded, want an error", bad)
		}
	}
}

func TestDataFormatFromName(t *testing.T) {
	for _, name := range []string{"json", "JSON", "jsonl", ".jsonl", "csv", ".CSV"} {
		if _, err := dataFormatFromName(name); err != nil {
			t.Errorf("dataFormatFromName(%q) error = %v", name, err)
		}
	}
	for _, name := range []string{"", "yaml", "parquet"} {
		if _, err := dataFormatFromName(name); err == nil {
			t.Errorf("dataFormatFromName(%q) succeeded, want an error", name)
		}
	}
}

// runRoot executes the real command tree with the given args, capturing output.
// Commands are expected to fail before dialing the control plane.
func runRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()

	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs(args)
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		cfg = config.DefaultConfig()
	})

	err := rootCmd.Execute()
	return buf.String(), err
}

// TestEvalRunRequiresPromptAndDataset covers the raw "error reading from
// server: EOF" a user got when --prompt or --dataset was omitted.
func TestEvalRunRequiresPromptAndDataset(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantFlag string
	}{
		{name: "no flags", args: []string{"eval", "run"}, wantFlag: "--prompt"},
		{name: "no dataset", args: []string{"eval", "run", "--prompt", "summarizer"}, wantFlag: "--dataset"},
		{
			name:     "no model",
			args:     []string{"eval", "run", "--prompt", "summarizer", "--dataset", "ds_1"},
			wantFlag: "--model",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runRoot(t, tc.args...)
			if err == nil {
				t.Fatalf("%v succeeded, want a validation error", tc.args)
			}
			if !strings.Contains(err.Error(), tc.wantFlag) {
				t.Errorf("error = %q, want it to mention %s", err.Error(), tc.wantFlag)
			}
			if strings.Contains(err.Error(), "EOF") {
				t.Errorf("error = %q, want a client-side validation message, not a transport error", err.Error())
			}
		})
	}
}

func TestDatasetsAddRequiresInput(t *testing.T) {
	_, err := runRoot(t, "datasets", "add", "ds_1")
	if err == nil {
		t.Fatal("datasets add without --example/--file succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "--example") {
		t.Errorf("error = %q, want it to mention --example", err.Error())
	}
}

func TestDatasetsAddRejectsBadExample(t *testing.T) {
	_, err := runRoot(t, "datasets", "add", "ds_1", "--example", "missing-separator")
	if err == nil {
		t.Fatal("datasets add with a malformed example succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "input=>expected") {
		t.Errorf("error = %q, want it to show the expected form", err.Error())
	}
}

// TestRootHelpExamplesAreRealCommands walks every example line in the root
// help text and checks it resolves to a real command with real flags. The
// previous help advertised "delos deploy create --prompt X --version 2",
// which failed with "unknown flag: --version".
func TestRootHelpExamplesAreRealCommands(t *testing.T) {
	for _, line := range strings.Split(rootCmd.Long, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "delos ") {
			continue
		}

		fields := strings.Fields(line)[1:]

		// Split the example into the command path and its flags.
		var path []string
		for _, f := range fields {
			if strings.HasPrefix(f, "-") {
				break
			}
			path = append(path, f)
		}

		cmd, _, err := rootCmd.Find(path)
		if err != nil {
			t.Errorf("example %q: %v", line, err)
			continue
		}
		// Find() falls back to the closest parent, so confirm the leaf is real.
		if cmd.HasSubCommands() && len(path) > 0 && cmd.Name() != path[len(path)-1] {
			t.Errorf("example %q: %q is not a command", line, strings.Join(path, " "))
			continue
		}

		for _, f := range fields {
			if !strings.HasPrefix(f, "-") {
				continue
			}
			token := strings.SplitN(f, "=", 2)[0]
			if strings.HasPrefix(token, "--") {
				name := strings.TrimPrefix(token, "--")
				if lookupFlag(cmd, name) == nil {
					t.Errorf("example %q: %q has no --%s flag", line, cmd.CommandPath(), name)
				}
				continue
			}
			// Shorthand: -o is the global output-format flag, so an example
			// must not use it to mean something else (such as a file name).
			short := strings.TrimPrefix(token, "-")
			if lookupShorthand(cmd, short) == nil {
				t.Errorf("example %q: %q has no -%s flag", line, cmd.CommandPath(), short)
			}
		}
	}
}

func lookupShorthand(cmd *cobra.Command, short string) interface{} {
	if f := cmd.Flags().ShorthandLookup(short); f != nil {
		return f
	}
	if f := cmd.InheritedFlags().ShorthandLookup(short); f != nil {
		return f
	}
	if f := rootCmd.PersistentFlags().ShorthandLookup(short); f != nil {
		return f
	}
	return nil
}

func lookupFlag(cmd *cobra.Command, name string) interface{} {
	if f := cmd.Flags().Lookup(name); f != nil {
		return f
	}
	if f := cmd.InheritedFlags().Lookup(name); f != nil {
		return f
	}
	if f := rootCmd.PersistentFlags().Lookup(name); f != nil {
		return f
	}
	return nil
}
