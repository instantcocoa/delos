package promptfile

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const validYAML = `name: Summarizer
slug: summarizer
description: Summarizes text
messages:
  - role: system
    content: You are terse.
  - role: user
    content: "Summarize: {{text}}"
variables:
  - name: text
    type: string
    required: true
config:
  temperature: 0.7
  max_tokens: 512
tags:
  - prod
metadata:
  owner: platform
`

// toProto builds the prompt the control plane would hand back after a push:
// the create request round-tripped through the server, plus the server-owned
// fields that FromProto is expected to drop.
func toProto(pf *PromptFile) *promptv1.Prompt {
	req := pf.ToCreateRequest()
	return &promptv1.Prompt{
		Id:            "prm_123",
		Version:       7,
		Name:          req.Name,
		Slug:          req.Slug,
		Description:   req.Description,
		Messages:      req.Messages,
		Variables:     req.Variables,
		DefaultConfig: req.DefaultConfig,
		Tags:          req.Tags,
		Metadata:      req.Metadata,
		CreatedBy:     "someone@example.com",
		CreatedAt:     timestamppb.Now(),
		UpdatedBy:     "someone@example.com",
		UpdatedAt:     timestamppb.Now(),
		Status:        promptv1.PromptStatus_PROMPT_STATUS_ACTIVE,
	}
}

// roundTrip walks the full push/pull path: file -> proto -> file -> YAML -> file.
func roundTrip(t *testing.T, pf *PromptFile) *PromptFile {
	t.Helper()
	back := FromProto(toProto(pf))
	if back == nil {
		t.Fatal("FromProto returned nil")
	}
	data, err := Marshal(back)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse of marshalled round trip failed: %v\n%s", err, data)
	}
	return parsed
}

// ---------------------------------------------------------------------------
// Round-trip fidelity. A prompt that survives push and pull unchanged must
// produce an empty SemanticDiff, otherwise `delos prompt push` reports the same
// difference on every run and pushes a new version forever.
// ---------------------------------------------------------------------------

func TestRoundTripFidelity(t *testing.T) {
	tests := []struct {
		name string
		pf   *PromptFile
	}{
		{
			name: "fully populated",
			pf:   base(),
		},
		{
			name: "minimal",
			pf: &PromptFile{
				Name:     "Minimal",
				Slug:     "minimal",
				Messages: []Message{{Role: "user", Content: "hi"}},
			},
		},
		{
			// The regression guard for the zero-elision bug: temperature 0 is a
			// meaningful and very common setting.
			name: "explicit zero temperature",
			pf: &PromptFile{
				Name:     "Zero",
				Slug:     "zero",
				Messages: []Message{{Role: "user", Content: "hi"}},
				Config:   &Config{Temperature: f64(0)},
			},
		},
		{
			name: "explicit zero top_p",
			pf: &PromptFile{
				Name:     "Zero",
				Slug:     "zero",
				Messages: []Message{{Role: "user", Content: "hi"}},
				Config:   &Config{TopP: f64(0)},
			},
		},
		{
			name: "explicit zero max_tokens",
			pf: &PromptFile{
				Name:     "Zero",
				Slug:     "zero",
				Messages: []Message{{Role: "user", Content: "hi"}},
				Config:   &Config{MaxTokens: i32(0)},
			},
		},
		{
			name: "zero temperature alongside other settings",
			pf: &PromptFile{
				Name:     "Zero",
				Slug:     "zero",
				Messages: []Message{{Role: "user", Content: "hi"}},
				Config:   &Config{Temperature: f64(0), MaxTokens: i32(256), Stop: []string{"END"}},
			},
		},
		{
			name: "config with only stop",
			pf: &PromptFile{
				Name:     "Stop",
				Slug:     "stop",
				Messages: []Message{{Role: "user", Content: "hi"}},
				Config:   &Config{Stop: []string{"END"}},
			},
		},
		{
			name: "config with only output_schema",
			pf: &PromptFile{
				Name:     "Schema",
				Slug:     "schema",
				Messages: []Message{{Role: "user", Content: "hi"}},
				Config:   &Config{OutputSchema: `{"type":"object"}`},
			},
		},
		{
			name: "empty config struct",
			pf: &PromptFile{
				Name:     "Empty",
				Slug:     "empty",
				Messages: []Message{{Role: "user", Content: "hi"}},
				Config:   &Config{},
			},
		},
		{
			name: "empty slices rather than nil",
			pf: &PromptFile{
				Name:      "Empties",
				Slug:      "empties",
				Messages:  []Message{{Role: "user", Content: "hi"}},
				Variables: []Variable{},
				Tags:      []string{},
				Metadata:  map[string]string{},
				Config:    &Config{Stop: []string{}},
			},
		},
		{
			name: "variable with an empty default",
			pf: &PromptFile{
				Name:      "Def",
				Slug:      "def",
				Messages:  []Message{{Role: "user", Content: "{{a}}"}},
				Variables: []Variable{{Name: "a", Default: strptr("")}},
			},
		},
		{
			name: "multiline content and unicode",
			pf: &PromptFile{
				Name:     "Unicode ✨",
				Slug:     "unicode",
				Messages: []Message{{Role: "system", Content: "line one\nline two\n\tindented ✨"}},
				Tags:     []string{"ünïcode"},
				Metadata: map[string]string{"note": "héllo"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := roundTrip(t, tc.pf)

			if changes := SemanticDiff(tc.pf, got); len(changes) != 0 {
				t.Fatalf("round trip is not diff-stable, push would loop forever: %v", changes)
			}
			// And it must stay stable: a second trip changes nothing more.
			if changes := SemanticDiff(got, roundTrip(t, got)); len(changes) != 0 {
				t.Fatalf("second round trip drifted: %v", changes)
			}

			if got.Name != tc.pf.Name || got.Slug != tc.pf.Slug || got.Description != tc.pf.Description {
				t.Errorf("identity fields drifted: %+v", got)
			}
			if len(got.Messages) != len(tc.pf.Messages) {
				t.Fatalf("messages = %v, want %v", got.Messages, tc.pf.Messages)
			}
			for i, m := range tc.pf.Messages {
				if got.Messages[i] != m {
					t.Errorf("messages[%d] = %+v, want %+v", i, got.Messages[i], m)
				}
			}
		})
	}
}

// TestRoundTripPreservesExplicitZeroTemperature pins the actual value, not just
// diff-stability: pulling a prompt whose temperature is 0 must write "0" into
// the file rather than silently dropping the field.
func TestRoundTripPreservesExplicitZeroTemperature(t *testing.T) {
	pf := &PromptFile{
		Name:     "Zero",
		Slug:     "zero",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Config:   &Config{Temperature: f64(0), MaxTokens: i32(256)},
	}

	got := roundTrip(t, pf)
	if got.Config == nil {
		t.Fatal("config was dropped entirely by the round trip")
	}
	if got.Config.Temperature == nil {
		t.Fatal("temperature 0 was elided; SemanticDiff would report config drift on every push")
	}
	if *got.Config.Temperature != 0 {
		t.Errorf("temperature = %v, want 0", *got.Config.Temperature)
	}

	data, err := Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "temperature: 0") {
		t.Errorf("marshalled file lost the explicit zero:\n%s", data)
	}
}

// max_tokens is the documented exception: the file schema requires >= 1, so a
// zero can only mean "unset" and must not be written back.
func TestRoundTripDropsZeroMaxTokens(t *testing.T) {
	pf := &PromptFile{
		Name:     "Zero",
		Slug:     "zero",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Config:   &Config{MaxTokens: i32(0), Temperature: f64(0.5)},
	}

	got := roundTrip(t, pf)
	if got.Config.MaxTokens != nil {
		t.Errorf("max_tokens = %d, want nil (0 is invalid per the schema)", *got.Config.MaxTokens)
	}
	// Writing it back would have produced a file that fails validation.
	data, err := Marshal(&PromptFile{
		Name: "x", Slug: "x",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Config:   &Config{MaxTokens: i32(0)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(data); err == nil {
		t.Error("expected max_tokens: 0 to fail schema validation")
	}
}

func TestFromProto(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if got := FromProto(nil); got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})

	t.Run("drops server-owned fields and maps the rest", func(t *testing.T) {
		p := toProto(base())
		got := FromProto(p)

		if got.Name != "Summarizer" || got.Slug != "summarizer" || got.Description != "Summarizes text" {
			t.Errorf("identity fields = %+v", got)
		}
		if len(got.Messages) != 2 || got.Messages[1].Role != "user" {
			t.Errorf("messages = %+v", got.Messages)
		}
		if len(got.Variables) != 2 {
			t.Fatalf("variables = %+v", got.Variables)
		}
		if got.Variables[0].Name != "text" || !got.Variables[0].Required || got.Variables[0].Type != "string" {
			t.Errorf("variables[0] = %+v", got.Variables[0])
		}
		if got.Variables[1].Default == nil || *got.Variables[1].Default != "neutral" {
			t.Errorf("variables[1].Default = %v", got.Variables[1].Default)
		}
		if !reflect.DeepEqual(got.Tags, []string{"prod", "text"}) {
			t.Errorf("tags = %v", got.Tags)
		}
		if got.Metadata["owner"] != "platform" {
			t.Errorf("metadata = %v", got.Metadata)
		}
		if got.Config == nil || *got.Config.MaxTokens != 512 || *got.Config.Temperature != 0.7 {
			t.Fatalf("config = %+v", got.Config)
		}
		if got.Config.OutputSchema != `{"type":"object"}` || !reflect.DeepEqual(got.Config.Stop, []string{"END"}) {
			t.Errorf("config stop/output_schema = %+v", got.Config)
		}
	})

	t.Run("no config", func(t *testing.T) {
		got := FromProto(&promptv1.Prompt{Name: "n", Slug: "s"})
		if got.Config != nil {
			t.Errorf("config = %+v, want nil", got.Config)
		}
		if got.Messages != nil || got.Variables != nil {
			t.Errorf("expected nil slices, got %+v / %+v", got.Messages, got.Variables)
		}
	})

	t.Run("derives a slug from the name when absent", func(t *testing.T) {
		got := FromProto(&promptv1.Prompt{Name: "My Great Prompt!"})
		if got.Slug != "my-great-prompt" {
			t.Errorf("slug = %q", got.Slug)
		}
	})

	t.Run("empty variable default stays unset", func(t *testing.T) {
		got := FromProto(&promptv1.Prompt{
			Name:      "n",
			Slug:      "s",
			Variables: []*promptv1.PromptVariable{{Name: "a", DefaultValue: ""}},
		})
		if got.Variables[0].Default != nil {
			t.Errorf("default = %q, want nil", *got.Variables[0].Default)
		}
	})

	t.Run("empty metadata map is not materialized", func(t *testing.T) {
		got := FromProto(&promptv1.Prompt{Name: "n", Slug: "s", Metadata: map[string]string{}})
		if got.Metadata != nil {
			t.Errorf("metadata = %v, want nil", got.Metadata)
		}
	})
}

// ---------------------------------------------------------------------------
// Requests
// ---------------------------------------------------------------------------

func TestToCreateRequest(t *testing.T) {
	pf := base()
	req := pf.ToCreateRequest()

	if req.Name != pf.Name || req.Slug != pf.Slug || req.Description != pf.Description {
		t.Errorf("identity fields = %+v", req)
	}
	if len(req.Messages) != 2 || req.Messages[0].Role != "system" || req.Messages[0].Content != "You are terse." {
		t.Errorf("messages = %+v", req.Messages)
	}
	if len(req.Variables) != 2 {
		t.Fatalf("variables = %+v", req.Variables)
	}
	if req.Variables[1].DefaultValue != "neutral" || req.Variables[1].Description != "voice" {
		t.Errorf("variables[1] = %+v", req.Variables[1])
	}
	if req.DefaultConfig == nil {
		t.Fatal("default config = nil")
	}
	if req.DefaultConfig.Temperature != 0.7 || req.DefaultConfig.MaxTokens != 512 || req.DefaultConfig.TopP != 0.9 {
		t.Errorf("default config = %+v", req.DefaultConfig)
	}
	if !reflect.DeepEqual(req.DefaultConfig.Stop, []string{"END"}) || req.DefaultConfig.OutputSchema == "" {
		t.Errorf("default config stop/output_schema = %+v", req.DefaultConfig)
	}
	if !reflect.DeepEqual(req.Tags, pf.Tags) || !reflect.DeepEqual(req.Metadata, pf.Metadata) {
		t.Errorf("tags/metadata = %v / %v", req.Tags, req.Metadata)
	}
}

func TestToCreateRequestEmptyOptionals(t *testing.T) {
	pf := &PromptFile{Name: "n", Slug: "s"}
	req := pf.ToCreateRequest()

	if len(req.Messages) != 0 {
		t.Errorf("messages = %+v", req.Messages)
	}
	if req.Variables != nil {
		t.Errorf("variables = %+v, want nil", req.Variables)
	}
	if req.DefaultConfig != nil {
		t.Errorf("default config = %+v, want nil", req.DefaultConfig)
	}
}

// An explicit zero must still be transmitted as a config message, otherwise the
// server never learns about it.
func TestToCreateRequestSendsExplicitZeroConfig(t *testing.T) {
	pf := &PromptFile{Name: "n", Slug: "s", Config: &Config{Temperature: f64(0)}}
	req := pf.ToCreateRequest()
	if req.DefaultConfig == nil {
		t.Fatal("default config = nil, want a config carrying temperature 0")
	}
	if req.DefaultConfig.Temperature != 0 {
		t.Errorf("temperature = %v, want 0", req.DefaultConfig.Temperature)
	}
}

func TestToUpdateRequest(t *testing.T) {
	pf := base()
	req := pf.ToUpdateRequest("prm_123", "because")

	if req.Id != "prm_123" || req.ChangeDescription != "because" {
		t.Errorf("id/change description = %q / %q", req.Id, req.ChangeDescription)
	}
	if req.Description != pf.Description {
		t.Errorf("description = %q", req.Description)
	}
	if len(req.Messages) != 2 || len(req.Variables) != 2 || req.DefaultConfig == nil {
		t.Errorf("content fields = %+v", req)
	}
	if !reflect.DeepEqual(req.Tags, pf.Tags) || !reflect.DeepEqual(req.Metadata, pf.Metadata) {
		t.Errorf("tags/metadata = %v / %v", req.Tags, req.Metadata)
	}
}

// ---------------------------------------------------------------------------
// Parse / Marshal / Load / Save
// ---------------------------------------------------------------------------

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr string
	}{
		{name: "valid", in: validYAML},
		{name: "empty", in: "", wantErr: "prompt file is empty"},
		{name: "whitespace only", in: "   \n\t\n", wantErr: "prompt file is empty"},
		{name: "comment only", in: "# nothing here\n", wantErr: "document is empty"},
		{name: "bad yaml", in: "name: [unclosed\n", wantErr: "invalid YAML"},
		{name: "tabs are invalid yaml", in: "name:\n\t- x\n", wantErr: "invalid YAML"},
		{name: "not a mapping", in: "- a\n- b\n", wantErr: "Invalid type"},
		{name: "missing name", in: "slug: s\nmessages:\n  - {role: user, content: hi}\n", wantErr: "name is required"},
		{name: "missing slug", in: "name: n\nmessages:\n  - {role: user, content: hi}\n", wantErr: "slug is required"},
		{name: "missing messages", in: "name: n\nslug: s\n", wantErr: "messages is required"},
		{name: "no messages", in: "name: n\nslug: s\nmessages: []\n", wantErr: "at least 1 items"},
		{
			name:    "unknown field",
			in:      "name: n\nslug: s\nmessages:\n  - {role: user, content: hi}\nnope: 1\n",
			wantErr: "Additional property nope is not allowed",
		},
		{
			name:    "bad role",
			in:      "name: n\nslug: s\nmessages:\n  - {role: robot, content: hi}\n",
			wantErr: "messages.0.role",
		},
		{
			name:    "wrong scalar type",
			in:      "name: 12\nslug: s\nmessages:\n  - {role: user, content: hi}\n",
			wantErr: "Invalid type",
		},
		{
			name:    "bad slug pattern",
			in:      "name: n\nslug: Not A Slug\nmessages:\n  - {role: user, content: hi}\n",
			wantErr: "slug",
		},
		{
			name:    "temperature out of range",
			in:      "name: n\nslug: s\nmessages:\n  - {role: user, content: hi}\nconfig: {temperature: 5}\n",
			wantErr: "config.temperature",
		},
		{
			name:    "bad variable name",
			in:      "name: n\nslug: s\nmessages:\n  - {role: user, content: hi}\nvariables:\n  - {name: \"1bad\"}\n",
			wantErr: "variables.0.name",
		},
		{
			name:    "metadata value must be a string",
			in:      "name: n\nslug: s\nmessages:\n  - {role: user, content: hi}\nmetadata: {a: 1}\n",
			wantErr: "metadata.a",
		},
		{
			name:    "non-string mapping key",
			in:      "name: n\nslug: s\nmessages:\n  - {role: user, content: hi}\nmetadata:\n  1: x\n",
			wantErr: "mapping key",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse([]byte(tc.in))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got == nil {
					t.Fatal("got nil prompt file")
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got %+v", tc.wantErr, got)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestParseDecodesEverything(t *testing.T) {
	pf, err := Parse([]byte(validYAML))
	if err != nil {
		t.Fatal(err)
	}
	if pf.Name != "Summarizer" || pf.Slug != "summarizer" {
		t.Errorf("identity = %+v", pf)
	}
	if len(pf.Messages) != 2 || pf.Messages[1].Content != "Summarize: {{text}}" {
		t.Errorf("messages = %+v", pf.Messages)
	}
	if len(pf.Variables) != 1 || !pf.Variables[0].Required {
		t.Errorf("variables = %+v", pf.Variables)
	}
	if pf.Config == nil || *pf.Config.Temperature != 0.7 || *pf.Config.MaxTokens != 512 {
		t.Fatalf("config = %+v", pf.Config)
	}
	if pf.Config.TopP != nil {
		t.Errorf("top_p = %v, want nil", *pf.Config.TopP)
	}
	if !reflect.DeepEqual(pf.Tags, []string{"prod"}) || pf.Metadata["owner"] != "platform" {
		t.Errorf("tags/metadata = %v / %v", pf.Tags, pf.Metadata)
	}
}

func TestMarshal(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if _, err := Marshal(nil); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("emits the schema header and omits empties", func(t *testing.T) {
		data, err := Marshal(&PromptFile{
			Name:     "n",
			Slug:     "s",
			Messages: []Message{{Role: "user", Content: "hi"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		out := string(data)
		if !strings.HasPrefix(out, "# yaml-language-server: $schema="+SchemaURL+"\n") {
			t.Errorf("missing schema header:\n%s", out)
		}
		for _, absent := range []string{"description:", "variables:", "config:", "tags:", "metadata:"} {
			if strings.Contains(out, absent) {
				t.Errorf("expected %q to be omitted:\n%s", absent, out)
			}
		}
		if err := Validate(data); err != nil {
			t.Errorf("marshalled output does not validate: %v", err)
		}
	})

	t.Run("output re-parses", func(t *testing.T) {
		data, err := Marshal(base())
		if err != nil {
			t.Fatal(err)
		}
		got, err := Parse(data)
		if err != nil {
			t.Fatalf("Parse: %v\n%s", err, data)
		}
		if changes := SemanticDiff(base(), got); len(changes) != 0 {
			t.Errorf("marshal/parse is lossy: %v", changes)
		}
	})
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()

	good := filepath.Join(dir, "summarizer.prompt.yaml")
	if err := os.WriteFile(good, []byte(validYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.prompt.yaml")
	if err := os.WriteFile(bad, []byte("name: n\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("valid", func(t *testing.T) {
		pf, err := Load(good)
		if err != nil {
			t.Fatal(err)
		}
		if pf.Slug != "summarizer" {
			t.Errorf("slug = %q", pf.Slug)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := Load(filepath.Join(dir, "nope.prompt.yaml"))
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("error = %v, want os.ErrNotExist", err)
		}
	})

	t.Run("invalid content is reported with the path", func(t *testing.T) {
		_, err := Load(bad)
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), bad) {
			t.Errorf("error %q does not name the file", err)
		}
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("error %v is not a *ValidationError", err)
		}
	})

	t.Run("unreadable file", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("running as root: permission bits are not enforced")
		}
		locked := filepath.Join(dir, "locked.prompt.yaml")
		if err := os.WriteFile(locked, []byte(validYAML), 0o000); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(locked); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("error = %v, want a permission error", err)
		}
	})

	t.Run("directory", func(t *testing.T) {
		if _, err := Load(dir); err == nil {
			t.Fatal("expected an error reading a directory")
		}
	})
}

func TestSave(t *testing.T) {
	t.Run("writes into a created directory", func(t *testing.T) {
		dir := t.TempDir()
		path := PathFor(filepath.Join(dir, "nested", "deeper"), "summarizer")

		if err := Save(path, base()); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o644 {
			t.Errorf("mode = %v, want 0644", perm)
		}
		got, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if changes := SemanticDiff(base(), got); len(changes) != 0 {
			t.Errorf("save/load is lossy: %v", changes)
		}
	})

	t.Run("overwrites", func(t *testing.T) {
		dir := t.TempDir()
		path := PathFor(dir, "s")
		pf := &PromptFile{Name: "n", Slug: "s", Messages: []Message{{Role: "user", Content: "one"}}}
		if err := Save(path, pf); err != nil {
			t.Fatal(err)
		}
		pf.Messages[0].Content = "two"
		if err := Save(path, pf); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if got.Messages[0].Content != "two" {
			t.Errorf("content = %q", got.Messages[0].Content)
		}
	})

	t.Run("nil", func(t *testing.T) {
		if err := Save(filepath.Join(t.TempDir(), "x.prompt.yaml"), nil); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("refuses to write an invalid file", func(t *testing.T) {
		dir := t.TempDir()
		path := PathFor(dir, "invalid")
		// No messages: the schema requires at least one.
		err := Save(path, &PromptFile{Name: "n", Slug: "invalid"})
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), "refusing to write") {
			t.Errorf("error = %q", err)
		}
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Error("an invalid file was written to disk anyway")
		}
	})

	t.Run("unwritable parent", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("running as root: permission bits are not enforced")
		}
		dir := t.TempDir()
		locked := filepath.Join(dir, "locked")
		if err := os.Mkdir(locked, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

		if err := Save(PathFor(locked, "x"), base()); err == nil {
			t.Fatal("expected an error writing into a read-only directory")
		}
	})
}

// ---------------------------------------------------------------------------
// Paths and discovery
// ---------------------------------------------------------------------------

func TestFileNameAndPathFor(t *testing.T) {
	if got := FileName("summarizer"); got != "summarizer.prompt.yaml" {
		t.Errorf("FileName = %q", got)
	}
	if got := FileName(""); got != Ext {
		t.Errorf("FileName(\"\") = %q, want %q", got, Ext)
	}
	if got := PathFor("prompts", "summarizer"); got != filepath.Join("prompts", "summarizer.prompt.yaml") {
		t.Errorf("PathFor = %q", got)
	}
	if got := PathFor("", "s"); got != "s.prompt.yaml" {
		t.Errorf("PathFor with no dir = %q", got)
	}
	if got := PathFor("a/b/", "s"); got != filepath.Join("a", "b", "s.prompt.yaml") {
		t.Errorf("PathFor with a trailing separator = %q", got)
	}
}

func TestDiscover(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("zeta.prompt.yaml")
	write("alpha.prompt.yaml")
	write("notes.md")
	write("other.yaml")
	write("almost.prompt.yml")
	write(".prompt.yaml") // an empty slug still matches the suffix
	if err := os.Mkdir(filepath.Join(dir, "nested.prompt.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join("sub", "ignored.prompt.yaml"))

	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(dir, ".prompt.yaml"),
		filepath.Join(dir, "alpha.prompt.yaml"),
		filepath.Join(dir, "zeta.prompt.yaml"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Discover = %v, want %v", got, want)
	}
}

func TestDiscoverMissingDirectory(t *testing.T) {
	got, err := Discover(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("missing directory should not be an error, got %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestDiscoverEmptyDirectory(t *testing.T) {
	got, err := Discover(t.TempDir())
	if err != nil || got != nil {
		t.Errorf("got %v, %v; want nil, nil", got, err)
	}
}

func TestDiscoverNotADirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(path); err == nil {
		t.Fatal("expected an error")
	}
}

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*$`)

func TestSlugify(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"simple", "simple"},
		{"Summarizer", "summarizer"},
		{"My Great Prompt", "my-great-prompt"},
		{"  leading and trailing  ", "leading-and-trailing"},
		{"---dashes---", "dashes"},
		{"under_scores", "under-scores"},
		{"multiple   spaces", "multiple-spaces"},
		{"punctuation!@#$%^&*()", "punctuation"},
		{"mixed.CASE-123", "mixed-case-123"},
		{"123", "123"},
		{"!!!", ""},
		{"-", ""},
		{"a-b", "a-b"},
		{"a  --  b", "a-b"},
		{"tabs\tand\nnewlines", "tabs-and-newlines"},
		{"emoji ✨ prompt", "emoji-prompt"},
		{"Ünïcode Prompt", "n-code-prompt"},
		{"日本語", ""},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got := Slugify(tc.in)
			if got != tc.want {
				t.Errorf("Slugify(%q) = %q, want %q", tc.in, got, tc.want)
			}
			// Whatever it produces must be a slug the schema accepts, or empty.
			if got != "" && !slugPattern.MatchString(got) {
				t.Errorf("Slugify(%q) = %q, which the schema rejects", tc.in, got)
			}
			if Slugify(got) != got {
				t.Errorf("Slugify is not idempotent: Slugify(%q) = %q", got, Slugify(got))
			}
		})
	}
}

func TestConfigIsZero(t *testing.T) {
	tests := []struct {
		name string
		cfg  *Config
		want bool
	}{
		{"nil", nil, true},
		{"empty", &Config{}, true},
		{"empty stop slice", &Config{Stop: []string{}}, true},
		{"explicit zero temperature is set", &Config{Temperature: f64(0)}, false},
		{"explicit zero max_tokens is set", &Config{MaxTokens: i32(0)}, false},
		{"explicit zero top_p is set", &Config{TopP: f64(0)}, false},
		{"stop", &Config{Stop: []string{"END"}}, false},
		{"output schema", &Config{OutputSchema: "{}"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.isZero(); got != tc.want {
				t.Errorf("isZero() = %v, want %v", got, tc.want)
			}
		})
	}
}
