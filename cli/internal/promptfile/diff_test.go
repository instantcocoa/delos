package promptfile

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// base returns a fully-populated prompt file that every diff case mutates a
// single aspect of, so an assertion of "exactly one change" is meaningful.
func base() *PromptFile {
	return &PromptFile{
		Name:        "Summarizer",
		Slug:        "summarizer",
		Description: "Summarizes text",
		Messages: []Message{
			{Role: "system", Content: "You are terse."},
			{Role: "user", Content: "Summarize: {{text}}"},
		},
		Variables: []Variable{
			{Name: "text", Type: "string", Required: true, Description: "input"},
			{Name: "tone", Type: "string", Description: "voice", Default: strptr("neutral")},
		},
		Config: &Config{
			Temperature:  f64(0.7),
			MaxTokens:    i32(512),
			TopP:         f64(0.9),
			Stop:         []string{"END"},
			OutputSchema: `{"type":"object"}`,
		},
		Tags:     []string{"prod", "text"},
		Metadata: map[string]string{"owner": "platform", "tier": "1"},
	}
}

func strptr(s string) *string { return &s }
func f64(f float64) *float64  { return &f }
func i32(i int32) *int32      { return &i }

// fields returns the Change.Field values in order, for compact assertions.
func fields(changes []Change) []string {
	if len(changes) == 0 {
		return nil
	}
	out := make([]string, len(changes))
	for i, c := range changes {
		out[i] = c.Field
	}
	return out
}

func TestSemanticDiff(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*PromptFile)
		wantFields []string
		wantKind   string // kind expected on wantFields[0], "" to skip
		wantDetail string // substring expected in the first change's Detail
	}{
		{
			name:       "no change",
			mutate:     func(*PromptFile) {},
			wantFields: nil,
		},
		{
			name:       "name changed",
			mutate:     func(p *PromptFile) { p.Name = "Condenser" },
			wantFields: []string{"name"},
			wantKind:   "changed",
			wantDetail: `"Summarizer" -> "Condenser"`,
		},
		{
			name:       "description changed",
			mutate:     func(p *PromptFile) { p.Description = "Shortens text" },
			wantFields: []string{"description"},
			wantKind:   "changed",
		},
		{
			name:       "description removed",
			mutate:     func(p *PromptFile) { p.Description = "" },
			wantFields: []string{"description"},
			wantKind:   "changed",
		},
		{
			name: "message content edited",
			mutate: func(p *PromptFile) {
				p.Messages[0].Content = "You are verbose."
			},
			wantFields: []string{"messages[0]"},
			wantKind:   "changed",
			wantDetail: "content",
		},
		{
			name: "message whitespace only",
			mutate: func(p *PromptFile) {
				p.Messages[0].Content = "You   are\n terse. "
			},
			wantFields: []string{"messages[0]"},
			wantKind:   "whitespace",
			wantDetail: "whitespace-only",
		},
		{
			name: "message role changed",
			mutate: func(p *PromptFile) {
				p.Messages[0].Role = "assistant"
			},
			wantFields: []string{"messages[0]"},
			wantKind:   "changed",
			wantDetail: `role "system" -> "assistant"`,
		},
		{
			name: "role and content both changed reports role only",
			mutate: func(p *PromptFile) {
				p.Messages[0].Role = "assistant"
				p.Messages[0].Content = "different"
			},
			wantFields: []string{"messages[0]"},
			wantKind:   "changed",
			wantDetail: "role",
		},
		{
			name: "message added",
			mutate: func(p *PromptFile) {
				p.Messages = append(p.Messages, Message{Role: "assistant", Content: "Sure."})
			},
			wantFields: []string{"messages[2]"},
			wantKind:   "added",
			wantDetail: "assistant message",
		},
		{
			name: "message removed",
			mutate: func(p *PromptFile) {
				p.Messages = p.Messages[:1]
			},
			wantFields: []string{"messages[1]"},
			wantKind:   "removed",
			wantDetail: "user message",
		},
		{
			name: "messages reordered",
			mutate: func(p *PromptFile) {
				p.Messages[0], p.Messages[1] = p.Messages[1], p.Messages[0]
			},
			// Both positions differ by role, so both are reported.
			wantFields: []string{"messages[0]", "messages[1]"},
			wantKind:   "changed",
		},
		{
			name: "variable added",
			mutate: func(p *PromptFile) {
				p.Variables = append(p.Variables, Variable{Name: "lang", Type: "string"})
			},
			wantFields: []string{"variables.lang"},
			wantKind:   "added",
		},
		{
			name: "variable removed",
			mutate: func(p *PromptFile) {
				p.Variables = p.Variables[:1]
			},
			wantFields: []string{"variables.tone"},
			wantKind:   "removed",
		},
		{
			name: "variable type changed",
			mutate: func(p *PromptFile) {
				p.Variables[0].Type = "json"
			},
			wantFields: []string{"variables.text.type"},
			wantKind:   "changed",
			wantDetail: "string -> json",
		},
		{
			name: "variable type cleared reports unset",
			mutate: func(p *PromptFile) {
				p.Variables[0].Type = ""
			},
			wantFields: []string{"variables.text.type"},
			wantKind:   "changed",
			wantDetail: "string -> unset",
		},
		{
			name: "variable required changed",
			mutate: func(p *PromptFile) {
				p.Variables[0].Required = false
			},
			wantFields: []string{"variables.text.required"},
			wantKind:   "changed",
			wantDetail: "true -> false",
		},
		{
			name: "variable description changed",
			mutate: func(p *PromptFile) {
				p.Variables[0].Description = "the source document"
			},
			wantFields: []string{"variables.text.description"},
			wantKind:   "changed",
		},
		{
			name: "variable default changed",
			mutate: func(p *PromptFile) {
				p.Variables[1].Default = strptr("formal")
			},
			wantFields: []string{"variables.tone.default"},
			wantKind:   "changed",
			wantDetail: `"neutral" -> "formal"`,
		},
		{
			name: "variable default removed",
			mutate: func(p *PromptFile) {
				p.Variables[1].Default = nil
			},
			wantFields: []string{"variables.tone.default"},
			wantKind:   "changed",
			wantDetail: `"neutral" -> ""`,
		},
		{
			name: "variable order change is not a semantic change",
			mutate: func(p *PromptFile) {
				p.Variables[0], p.Variables[1] = p.Variables[1], p.Variables[0]
			},
			wantFields: nil,
		},
		{
			name: "several variable attributes at once",
			mutate: func(p *PromptFile) {
				p.Variables[0].Type = "json"
				p.Variables[0].Required = false
				p.Variables[0].Description = "doc"
			},
			wantFields: []string{"variables.text.type", "variables.text.required", "variables.text.description"},
		},
		{
			name:       "tag added",
			mutate:     func(p *PromptFile) { p.Tags = append(p.Tags, "beta") },
			wantFields: []string{"tags"},
			wantKind:   "added",
			wantDetail: `tag "beta"`,
		},
		{
			name:       "tag removed",
			mutate:     func(p *PromptFile) { p.Tags = []string{"prod"} },
			wantFields: []string{"tags"},
			wantKind:   "removed",
			wantDetail: `tag "text"`,
		},
		{
			name:       "all tags removed",
			mutate:     func(p *PromptFile) { p.Tags = nil },
			wantFields: []string{"tags", "tags"},
			wantKind:   "removed",
		},
		{
			name:       "tags reordered",
			mutate:     func(p *PromptFile) { p.Tags = []string{"text", "prod"} },
			wantFields: []string{"tags"},
			wantKind:   "changed",
			wantDetail: "order changed",
		},
		{
			name:       "identical tags in a fresh slice are not a change",
			mutate:     func(p *PromptFile) { p.Tags = []string{"prod", "text"} },
			wantFields: nil,
		},
		{
			name:       "metadata value changed",
			mutate:     func(p *PromptFile) { p.Metadata["owner"] = "search" },
			wantFields: []string{"metadata.owner"},
			wantKind:   "changed",
			wantDetail: `"platform" -> "search"`,
		},
		{
			name:       "metadata key added",
			mutate:     func(p *PromptFile) { p.Metadata["oncall"] = "ann" },
			wantFields: []string{"metadata.oncall"},
			wantKind:   "added",
		},
		{
			name:       "metadata key removed",
			mutate:     func(p *PromptFile) { delete(p.Metadata, "tier") },
			wantFields: []string{"metadata.tier"},
			wantKind:   "removed",
		},
		{
			name:       "metadata cleared",
			mutate:     func(p *PromptFile) { p.Metadata = nil },
			wantFields: []string{"metadata.owner", "metadata.tier"},
			wantKind:   "removed",
		},
		{
			name:       "config temperature changed",
			mutate:     func(p *PromptFile) { p.Config.Temperature = f64(0.2) },
			wantFields: []string{"config.temperature"},
			wantKind:   "changed",
			wantDetail: "0.7 -> 0.2",
		},
		{
			name:       "config max_tokens changed",
			mutate:     func(p *PromptFile) { p.Config.MaxTokens = i32(1024) },
			wantFields: []string{"config.max_tokens"},
			wantKind:   "changed",
			wantDetail: "512 -> 1024",
		},
		{
			name:       "config max_tokens removed",
			mutate:     func(p *PromptFile) { p.Config.MaxTokens = nil },
			wantFields: []string{"config.max_tokens"},
			wantKind:   "changed",
			wantDetail: "512 -> 0",
		},
		{
			name:       "config top_p changed",
			mutate:     func(p *PromptFile) { p.Config.TopP = f64(0.5) },
			wantFields: []string{"config.top_p"},
			wantKind:   "changed",
			wantDetail: "0.9 -> 0.5",
		},
		{
			name:       "config stop changed",
			mutate:     func(p *PromptFile) { p.Config.Stop = []string{"END", "STOP"} },
			wantFields: []string{"config.stop"},
			wantKind:   "changed",
			wantDetail: "[END] -> [END, STOP]",
		},
		{
			name:       "config stop removed",
			mutate:     func(p *PromptFile) { p.Config.Stop = nil },
			wantFields: []string{"config.stop"},
			wantKind:   "changed",
		},
		{
			name:       "config stop reordered",
			mutate:     func(p *PromptFile) { p.Config.Stop = []string{"END", "X"} },
			wantFields: []string{"config.stop"},
			wantKind:   "changed",
		},
		{
			name:       "config output_schema changed",
			mutate:     func(p *PromptFile) { p.Config.OutputSchema = `{"type":"array"}` },
			wantFields: []string{"config.output_schema"},
			wantKind:   "changed",
		},
		{
			name:       "config output_schema removed",
			mutate:     func(p *PromptFile) { p.Config.OutputSchema = "" },
			wantFields: []string{"config.output_schema"},
			wantKind:   "changed",
		},
		{
			name:       "config removed entirely",
			mutate:     func(p *PromptFile) { p.Config = nil },
			wantFields: []string{"config"},
			wantKind:   "removed",
			wantDetail: "generation config removed",
		},
		{
			name: "every config field at once",
			mutate: func(p *PromptFile) {
				p.Config = &Config{
					Temperature:  f64(0.1),
					MaxTokens:    i32(8),
					TopP:         f64(0.1),
					Stop:         []string{"HALT"},
					OutputSchema: "{}",
				}
			},
			wantFields: []string{
				"config.temperature", "config.max_tokens", "config.top_p",
				"config.stop", "config.output_schema",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, b := base(), base()
			tc.mutate(b)

			got := SemanticDiff(a, b)
			if !reflect.DeepEqual(fields(got), tc.wantFields) {
				t.Fatalf("fields = %v, want %v (changes: %v)", fields(got), tc.wantFields, got)
			}
			if len(tc.wantFields) == 0 {
				return
			}
			if tc.wantKind != "" && got[0].Kind != tc.wantKind {
				t.Errorf("kind = %q, want %q (change: %v)", got[0].Kind, tc.wantKind, got[0])
			}
			if tc.wantDetail != "" && !strings.Contains(got[0].Detail, tc.wantDetail) {
				t.Errorf("detail = %q, want it to contain %q", got[0].Detail, tc.wantDetail)
			}

			// A diff must be symmetric in the sense that reversing the operands
			// still reports exactly the same fields; a field that only shows up
			// in one direction is a field whose edits get silently dropped.
			back := fields(SemanticDiff(b, a))
			sortedGot, sortedBack := append([]string(nil), fields(got)...), append([]string(nil), back...)
			sort.Strings(sortedGot)
			sort.Strings(sortedBack)
			if !reflect.DeepEqual(sortedGot, sortedBack) {
				t.Errorf("reversed diff fields = %v, want %v", sortedBack, sortedGot)
			}
		})
	}
}

// TestSemanticDiffAddedConfig covers the direction the table cannot express
// (base always has a config).
func TestSemanticDiffAddedConfig(t *testing.T) {
	a, b := base(), base()
	a.Config = nil

	got := SemanticDiff(a, b)
	if len(got) != 1 || got[0].Field != "config" || got[0].Kind != "added" {
		t.Fatalf("got %v, want a single added config change", got)
	}
}

// TestSemanticDiffZeroConfigIsNotPresent documents the proto3 limitation: an
// all-zero config carries no information the control plane can distinguish from
// no config, so it must not be reported as a difference. Reporting it is what
// produced the perpetual "config added or removed" diff.
func TestSemanticDiffZeroConfigIsNotPresent(t *testing.T) {
	cases := []struct {
		name string
		cfg  *Config
	}{
		{"nil", nil},
		{"empty struct", &Config{}},
		{"explicit zero temperature", &Config{Temperature: f64(0)}},
		{"explicit zero top_p", &Config{TopP: f64(0)}},
		{"explicit zero max_tokens", &Config{MaxTokens: i32(0)}},
		{"all zero", &Config{Temperature: f64(0), TopP: f64(0), MaxTokens: i32(0), Stop: []string{}}},
	}

	for _, x := range cases {
		for _, y := range cases {
			t.Run(x.name+" vs "+y.name, func(t *testing.T) {
				a, b := base(), base()
				a.Config, b.Config = x.cfg, y.cfg
				if got := SemanticDiff(a, b); len(got) != 0 {
					t.Fatalf("got %v, want no changes", got)
				}
			})
		}
	}
}

// A zero-valued numeric field still has to be distinguishable from a non-zero
// one in both directions - the ambiguity fix must not swallow real edits.
func TestSemanticDiffZeroVsNonZeroConfig(t *testing.T) {
	a, b := base(), base()
	a.Config = &Config{Temperature: f64(0), MaxTokens: i32(512)}
	b.Config = &Config{Temperature: f64(0.7), MaxTokens: i32(512)}

	got := SemanticDiff(a, b)
	if len(got) != 1 || got[0].Field != "config.temperature" {
		t.Fatalf("got %v, want config.temperature", got)
	}
	if want := "0 -> 0.7"; got[0].Detail != want {
		t.Errorf("detail = %q, want %q", got[0].Detail, want)
	}
}

func TestSemanticDiffDeterministicOrder(t *testing.T) {
	a, b := base(), base()
	b.Metadata = map[string]string{"a": "1", "b": "2", "c": "3", "d": "4", "e": "5"}
	b.Tags = []string{"x", "y", "z"}
	b.Variables = []Variable{{Name: "p"}, {Name: "q"}, {Name: "r"}}

	want := fields(SemanticDiff(a, b))
	for i := 0; i < 50; i++ {
		if got := fields(SemanticDiff(a, b)); !reflect.DeepEqual(got, want) {
			t.Fatalf("iteration %d: fields = %v, want %v", i, got, want)
		}
	}
	// Metadata keys must be sorted, not in map order.
	var meta []string
	for _, f := range want {
		if strings.HasPrefix(f, "metadata.") {
			meta = append(meta, f)
		}
	}
	if !sort.StringsAreSorted(meta) {
		t.Errorf("metadata fields not sorted: %v", meta)
	}
}

func TestSemanticDiffEmptyFiles(t *testing.T) {
	if got := SemanticDiff(&PromptFile{}, &PromptFile{}); len(got) != 0 {
		t.Fatalf("got %v, want no changes", got)
	}
}

func TestChangeString(t *testing.T) {
	c := Change{Field: "config.temperature", Kind: "changed", Detail: "0.7 -> 0.2"}
	if want := "changed config.temperature: 0.7 -> 0.2"; c.String() != want {
		t.Errorf("String() = %q, want %q", c.String(), want)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("abcdef", 3); got != "abc…" {
		t.Errorf("truncate = %q", got)
	}
	if got := truncate("abc", 3); got != "abc" {
		t.Errorf("truncate = %q", got)
	}
}

func TestRender(t *testing.T) {
	tests := []struct {
		name    string
		pf      *PromptFile
		vars    map[string]string
		want    []Message
		wantErr string
	}{
		{
			name: "required variable missing",
			pf: &PromptFile{
				Variables: []Variable{{Name: "text", Required: true}},
				Messages:  []Message{{Role: "user", Content: "{{text}}"}},
			},
			wantErr: `required variable "text" not provided`,
		},
		{
			name: "required variable satisfied by default",
			pf: &PromptFile{
				Variables: []Variable{{Name: "text", Required: true, Default: strptr("d")}},
				Messages:  []Message{{Role: "user", Content: "{{text}}"}},
			},
			want: []Message{{Role: "user", Content: "d"}},
		},
		{
			name: "default used when value absent",
			pf: &PromptFile{
				Variables: []Variable{{Name: "tone", Default: strptr("neutral")}},
				Messages:  []Message{{Role: "system", Content: "Be {{tone}}."}},
			},
			want: []Message{{Role: "system", Content: "Be neutral."}},
		},
		{
			name: "provided value overrides default",
			pf: &PromptFile{
				Variables: []Variable{{Name: "tone", Default: strptr("neutral")}},
				Messages:  []Message{{Role: "system", Content: "Be {{tone}}."}},
			},
			vars: map[string]string{"tone": "formal"},
			want: []Message{{Role: "system", Content: "Be formal."}},
		},
		{
			name: "optional variable without default is left untouched",
			pf: &PromptFile{
				Variables: []Variable{{Name: "tone"}},
				Messages:  []Message{{Role: "system", Content: "Be {{tone}}."}},
			},
			want: []Message{{Role: "system", Content: "Be {{tone}}."}},
		},
		{
			name: "undeclared placeholder is left untouched",
			pf: &PromptFile{
				Messages: []Message{{Role: "user", Content: "{{nope}}"}},
			},
			want: []Message{{Role: "user", Content: "{{nope}}"}},
		},
		{
			name: "multiple occurrences of one variable",
			pf: &PromptFile{
				Variables: []Variable{{Name: "x", Required: true}},
				Messages:  []Message{{Role: "user", Content: "{{x}}-{{x}}-{{x}}"}},
			},
			vars: map[string]string{"x": "1"},
			want: []Message{{Role: "user", Content: "1-1-1"}},
		},
		{
			name: "substituted value is not re-expanded",
			pf: &PromptFile{
				Variables: []Variable{{Name: "a", Required: true}, {Name: "b", Required: true}},
				Messages:  []Message{{Role: "user", Content: "{{a}} and {{b}}"}},
			},
			vars: map[string]string{"a": "{{b}}", "b": "BEE"},
			want: []Message{{Role: "user", Content: "{{b}} and BEE"}},
		},
		{
			name: "value containing a placeholder for itself is not re-expanded",
			pf: &PromptFile{
				Variables: []Variable{{Name: "a", Required: true}},
				Messages:  []Message{{Role: "user", Content: "<{{a}}>"}},
			},
			vars: map[string]string{"a": "{{a}}"},
			want: []Message{{Role: "user", Content: "<{{a}}>"}},
		},
		{
			name: "value from a default is not re-expanded either",
			pf: &PromptFile{
				Variables: []Variable{{Name: "a", Default: strptr("{{b}}")}, {Name: "b", Default: strptr("BEE")}},
				Messages:  []Message{{Role: "user", Content: "{{a}}"}},
			},
			want: []Message{{Role: "user", Content: "{{b}}"}},
		},
		{
			name: "unterminated placeholder",
			pf: &PromptFile{
				Variables: []Variable{{Name: "a", Default: strptr("A")}},
				Messages:  []Message{{Role: "user", Content: "{{a}} {{a"}},
			},
			want: []Message{{Role: "user", Content: "A {{a"}},
		},
		{
			name: "stray braces",
			pf: &PromptFile{
				Messages: []Message{{Role: "user", Content: "}}{{"}},
			},
			want: []Message{{Role: "user", Content: "}}{{"}},
		},
		{
			name: "empty placeholder name",
			pf: &PromptFile{
				Messages: []Message{{Role: "user", Content: "{{}}"}},
			},
			want: []Message{{Role: "user", Content: "{{}}"}},
		},
		{
			name: "placeholder with surrounding spaces is not a placeholder",
			pf: &PromptFile{
				Variables: []Variable{{Name: "a", Default: strptr("A")}},
				Messages:  []Message{{Role: "user", Content: "{{ a }}"}},
			},
			want: []Message{{Role: "user", Content: "{{ a }}"}},
		},
		{
			name: "empty value substitutes to nothing",
			pf: &PromptFile{
				Variables: []Variable{{Name: "a", Default: strptr("")}},
				Messages:  []Message{{Role: "user", Content: "[{{a}}]"}},
			},
			want: []Message{{Role: "user", Content: "[]"}},
		},
		{
			name: "all messages are rendered",
			pf: &PromptFile{
				Variables: []Variable{{Name: "x", Required: true}},
				Messages: []Message{
					{Role: "system", Content: "s {{x}}"},
					{Role: "user", Content: "u {{x}}"},
				},
			},
			vars: map[string]string{"x": "v"},
			want: []Message{{Role: "system", Content: "s v"}, {Role: "user", Content: "u v"}},
		},
		{
			name: "no messages",
			pf:   &PromptFile{},
			want: []Message{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Render(tc.pf, tc.vars)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got messages %v", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %q, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

// Render must be deterministic no matter how many variables are in play: the
// old implementation iterated the value map, so a value containing another
// variable's placeholder expanded or not depending on map order.
func TestRenderDeterministic(t *testing.T) {
	pf := &PromptFile{
		Messages: []Message{{Role: "user", Content: "{{a}} {{b}} {{c}} {{d}} {{e}}"}},
		Variables: []Variable{
			{Name: "a", Default: strptr("{{b}}")},
			{Name: "b", Default: strptr("{{c}}")},
			{Name: "c", Default: strptr("{{d}}")},
			{Name: "d", Default: strptr("{{e}}")},
			{Name: "e", Default: strptr("{{a}}")},
		},
	}
	const want = "{{b}} {{c}} {{d}} {{e}} {{a}}"
	for i := 0; i < 200; i++ {
		got, err := Render(pf, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got[0].Content != want {
			t.Fatalf("iteration %d: got %q, want %q", i, got[0].Content, want)
		}
	}
}

func TestRenderErrorNamesTheVariable(t *testing.T) {
	pf := &PromptFile{
		Variables: []Variable{{Name: "customer_id", Required: true}},
		Messages:  []Message{{Role: "user", Content: "{{customer_id}}"}},
	}
	_, err := Render(pf, map[string]string{"other": "x"})
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"customer_id", "--var customer_id="} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestSquashSpace(t *testing.T) {
	if got := squashSpace(" a \n b\t c "); got != "a b c" {
		t.Errorf("squashSpace = %q", got)
	}
}

func TestFloatStr(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{{0, "0"}, {0.7, "0.7"}, {1, "1"}, {0.1234, "0.1234"}} {
		if got := floatStr(tc.in); got != tc.want {
			t.Errorf("floatStr(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEqualStrings(t *testing.T) {
	cases := []struct {
		a, b []string
		want bool
	}{
		{nil, nil, true},
		{nil, []string{}, true},
		{[]string{"a"}, []string{"a"}, true},
		{[]string{"a"}, []string{"b"}, false},
		{[]string{"a"}, []string{"a", "b"}, false},
		{[]string{"a", "b"}, []string{"b", "a"}, false},
	}
	for _, tc := range cases {
		if got := equalStrings(tc.a, tc.b); got != tc.want {
			t.Errorf("equalStrings(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func ExampleChange_String() {
	fmt.Println(Change{Field: "name", Kind: "changed", Detail: `"a" -> "b"`})
	// Output: changed name: "a" -> "b"
}

// Duplicate variable names are not schema-valid but must not make the diff
// report the same variable twice.
func TestSemanticDiffDuplicateVariableNames(t *testing.T) {
	a := &PromptFile{Variables: []Variable{{Name: "x"}, {Name: "x"}}}
	b := &PromptFile{}

	if got := SemanticDiff(a, b); len(got) != 1 || got[0].Field != "variables.x" || got[0].Kind != "removed" {
		t.Fatalf("got %v, want a single removed variable", got)
	}
	if got := SemanticDiff(b, a); len(got) != 1 || got[0].Kind != "added" {
		t.Fatalf("got %v, want a single added variable", got)
	}
}

func TestSubstituteNestedBraces(t *testing.T) {
	values := map[string]string{"a": "A", "b": "B", "a{{b": "NOPE"}
	cases := []struct{ in, want string }{
		{"", ""},
		{"no placeholders", "no placeholders"},
		{"{{a}}", "A"},
		{"{{a{{b}}}}", "{{aB}}"}, // the inner placeholder is the real one
		{"{{{{a}}}}", "{{A}}"},
		{"{{a", "{{a"},
		{"{{", "{{"},
		{"}}", "}}"},
		{"{{unknown}}{{a}}", "{{unknown}}A"},
	}
	for _, tc := range cases {
		if got := substitute(tc.in, values); got != tc.want {
			t.Errorf("substitute(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
