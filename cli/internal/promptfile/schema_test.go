package promptfile

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docsSchemaPath is the published copy that prompt.schema.json mirrors.
const docsSchemaPath = "../../../docs/schemas/prompt.schema.json"

// TestEmbeddedSchemaMatchesPublishedCopy is the test the doc comment on
// schemaJSON claims exists. The embedded copy is what actually validates every
// prompt file, while docs/schemas/prompt.schema.json is what editors fetch from
// the SchemaURL: if they drift, the CLI and the user's editor disagree about
// what a valid prompt file is.
func TestEmbeddedSchemaMatchesPublishedCopy(t *testing.T) {
	published, err := os.ReadFile(filepath.Clean(docsSchemaPath))
	if err != nil {
		t.Fatalf("cannot read the published schema: %v", err)
	}
	if !bytes.Equal(schemaJSON, published) {
		t.Errorf("cli/internal/promptfile/prompt.schema.json and %s have drifted; "+
			"copy one over the other (embedded %d bytes, published %d bytes)",
			docsSchemaPath, len(schemaJSON), len(published))
	}
}

// The SchemaURL must point at the file the embedded copy mirrors, otherwise the
// header Marshal writes sends editors to the wrong document.
func TestSchemaURLMatchesSchemaID(t *testing.T) {
	var doc struct {
		ID string `json:"$id"`
	}
	if err := json.Unmarshal(schemaJSON, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.ID != SchemaURL {
		t.Errorf("$id = %q, want SchemaURL %q", doc.ID, SchemaURL)
	}
	if !strings.HasSuffix(SchemaURL, "docs/schemas/prompt.schema.json") {
		t.Errorf("SchemaURL %q does not point at the published path", SchemaURL)
	}
}

func TestSchemaIsValidJSONAndCompiles(t *testing.T) {
	var doc map[string]interface{}
	if err := json.Unmarshal(schemaJSON, &doc); err != nil {
		t.Fatalf("embedded schema is not valid JSON: %v", err)
	}
	if _, err := compiledSchema(); err != nil {
		t.Fatalf("embedded schema does not compile: %v", err)
	}
}

// Schema returns a copy: a caller that mutates the result must not corrupt
// validation for the rest of the process.
func TestSchemaReturnsACopy(t *testing.T) {
	first := Schema()
	if len(first) == 0 {
		t.Fatal("Schema() returned nothing")
	}
	if !bytes.Equal(first, schemaJSON) {
		t.Fatal("Schema() does not match the embedded document")
	}

	first[0] = 'X'
	if second := Schema(); second[0] == 'X' {
		t.Error("Schema() handed out the underlying buffer")
	}
	if schemaJSON[0] == 'X' {
		t.Error("mutating the result of Schema() corrupted the embedded document")
	}
}

func TestValidationErrorMessage(t *testing.T) {
	one := &ValidationError{Problems: []string{"slug is required"}}
	if got, want := one.Error(), "invalid prompt file: slug is required"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	many := &ValidationError{Problems: []string{"a", "b"}}
	got := many.Error()
	for _, want := range []string{"invalid prompt file:", "\n  - a", "\n  - b"} {
		if !strings.Contains(got, want) {
			t.Errorf("Error() = %q, want it to contain %q", got, want)
		}
	}
}

// Problems are sorted so the same broken file always reports the same way.
func TestValidateProblemsAreSortedAndStable(t *testing.T) {
	data := []byte("zebra: 1\nalpha: 2\n")

	var ve *ValidationError
	err := Validate(data)
	if err == nil {
		t.Fatal("expected an error")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("error is %T, want *ValidationError", err)
	}
	for i := 1; i < len(ve.Problems); i++ {
		if ve.Problems[i-1] > ve.Problems[i] {
			t.Fatalf("problems are not sorted: %v", ve.Problems)
		}
	}
	for i := 0; i < 10; i++ {
		again := Validate(data)
		if again.Error() != err.Error() {
			t.Fatalf("validation output is not stable:\n%v\n%v", err, again)
		}
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr string
	}{
		{name: "valid", in: validYAML},
		{
			name: "valid with every optional field",
			in: `name: Full
slug: full_prompt
description: everything
messages:
  - role: system
    content: s
  - role: assistant
    content: a
  - role: user
    content: u
variables:
  - name: _x1
    type: json
    required: false
    description: d
    default: "{}"
config:
  temperature: 0
  max_tokens: 1
  top_p: 0
  stop: ["END"]
  output_schema: "{}"
tags: [a, b]
metadata:
  k: v
`,
		},
		{name: "empty document", in: "", wantErr: "document is empty"},
		{name: "null document", in: "null\n", wantErr: "document is empty"},
		{name: "broken yaml", in: "a: [\n", wantErr: "invalid YAML"},
		{name: "bad role", in: "name: n\nslug: s\nmessages: [{role: nope, content: c}]\n", wantErr: "messages.0.role"},
		{name: "message missing content", in: "name: n\nslug: s\nmessages: [{role: user}]\n", wantErr: "content is required"},
		{name: "extra message field", in: "name: n\nslug: s\nmessages: [{role: user, content: c, x: 1}]\n", wantErr: "Additional property x"},
		{name: "empty name", in: "name: \"\"\nslug: s\nmessages: [{role: user, content: c}]\n", wantErr: "name"},
		{name: "uppercase slug", in: "name: n\nslug: Bad\nmessages: [{role: user, content: c}]\n", wantErr: "slug"},
		{name: "slug with a leading dash", in: "name: n\nslug: -bad\nmessages: [{role: user, content: c}]\n", wantErr: "slug"},
		{name: "messages not an array", in: "name: n\nslug: s\nmessages: hi\n", wantErr: "messages"},
		{name: "max_tokens zero", in: "name: n\nslug: s\nmessages: [{role: user, content: c}]\nconfig: {max_tokens: 0}\n", wantErr: "config.max_tokens"},
		{name: "max_tokens fractional", in: "name: n\nslug: s\nmessages: [{role: user, content: c}]\nconfig: {max_tokens: 1.5}\n", wantErr: "config.max_tokens"},
		{name: "top_p above one", in: "name: n\nslug: s\nmessages: [{role: user, content: c}]\nconfig: {top_p: 2}\n", wantErr: "config.top_p"},
		{name: "temperature negative", in: "name: n\nslug: s\nmessages: [{role: user, content: c}]\nconfig: {temperature: -1}\n", wantErr: "config.temperature"},
		{name: "unknown config key", in: "name: n\nslug: s\nmessages: [{role: user, content: c}]\nconfig: {seed: 1}\n", wantErr: "Additional property seed"},
		{name: "stop not strings", in: "name: n\nslug: s\nmessages: [{role: user, content: c}]\nconfig: {stop: [1]}\n", wantErr: "config.stop.0"},
		{name: "empty tag", in: "name: n\nslug: s\nmessages: [{role: user, content: c}]\ntags: [\"\"]\n", wantErr: "tags.0"},
		{name: "bad variable type", in: "name: n\nslug: s\nmessages: [{role: user, content: c}]\nvariables: [{name: a, type: date}]\n", wantErr: "variables.0.type"},
		{name: "variable default not a string", in: "name: n\nslug: s\nmessages: [{role: user, content: c}]\nvariables: [{name: a, default: 1}]\n", wantErr: "variables.0.default"},
		{name: "variable missing name", in: "name: n\nslug: s\nmessages: [{role: user, content: c}]\nvariables: [{type: string}]\n", wantErr: "name is required"},
		{name: "nested non-string key", in: "name: n\nslug: s\nmessages: [{role: user, content: c}]\nmetadata: {1: v}\n", wantErr: "mapping key"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate([]byte(tc.in))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestToJSONValue(t *testing.T) {
	t.Run("converts nested interface keys", func(t *testing.T) {
		in := map[interface{}]interface{}{
			"a": []interface{}{map[interface{}]interface{}{"b": 1}},
		}
		got, err := toJSONValue(in)
		if err != nil {
			t.Fatal(err)
		}
		out, ok := got.(map[string]interface{})
		if !ok {
			t.Fatalf("got %T, want map[string]interface{}", got)
		}
		list := out["a"].([]interface{})
		if _, ok := list[0].(map[string]interface{}); !ok {
			t.Fatalf("nested value = %T, want map[string]interface{}", list[0])
		}
	})

	t.Run("rejects a non-string key", func(t *testing.T) {
		if _, err := toJSONValue(map[interface{}]interface{}{1: "x"}); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("propagates errors from nested values", func(t *testing.T) {
		for _, in := range []interface{}{
			map[string]interface{}{"a": map[interface{}]interface{}{1: "x"}},
			map[interface{}]interface{}{"a": map[interface{}]interface{}{1: "x"}},
			[]interface{}{map[interface{}]interface{}{1: "x"}},
		} {
			if _, err := toJSONValue(in); err == nil {
				t.Errorf("expected an error for %#v", in)
			}
		}
	})

	t.Run("passes scalars through", func(t *testing.T) {
		for _, in := range []interface{}{nil, 1, "s", true, 1.5} {
			got, err := toJSONValue(in)
			if err != nil {
				t.Fatal(err)
			}
			if got != in {
				t.Errorf("toJSONValue(%v) = %v", in, got)
			}
		}
	})
}
