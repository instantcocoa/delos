package promptfile

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/xeipuuv/gojsonschema"
	"gopkg.in/yaml.v3"
)

// schemaJSON is the published JSON Schema for the prompt file format. It is a
// verbatim copy of docs/schemas/prompt.schema.json (enforced by a test),
// embedded so validation works offline.
//
//go:embed prompt.schema.json
var schemaJSON []byte

// Schema returns the raw JSON Schema document describing the file format.
func Schema() []byte {
	out := make([]byte, len(schemaJSON))
	copy(out, schemaJSON)
	return out
}

var (
	schemaOnce sync.Once
	schema     *gojsonschema.Schema
	schemaErr  error
)

func compiledSchema() (*gojsonschema.Schema, error) {
	schemaOnce.Do(func() {
		schema, schemaErr = gojsonschema.NewSchema(gojsonschema.NewBytesLoader(schemaJSON))
	})
	return schema, schemaErr
}

// ValidationError reports one or more JSON Schema violations.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	if len(e.Problems) == 1 {
		return "invalid prompt file: " + e.Problems[0]
	}
	return "invalid prompt file:\n  - " + strings.Join(e.Problems, "\n  - ")
}

// Validate checks YAML bytes against the embedded JSON Schema.
func Validate(data []byte) error {
	var raw interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("invalid YAML: %w", err)
	}
	if raw == nil {
		return &ValidationError{Problems: []string{"document is empty"}}
	}

	converted, err := toJSONValue(raw)
	if err != nil {
		return err
	}

	doc, err := json.Marshal(converted)
	if err != nil {
		return fmt.Errorf("failed to normalize prompt file: %w", err)
	}

	s, err := compiledSchema()
	if err != nil {
		return fmt.Errorf("failed to load prompt schema: %w", err)
	}

	result, err := s.Validate(gojsonschema.NewBytesLoader(doc))
	if err != nil {
		return fmt.Errorf("failed to validate prompt file: %w", err)
	}
	if result.Valid() {
		return nil
	}

	problems := make([]string, 0, len(result.Errors()))
	for _, e := range result.Errors() {
		field := e.Field()
		if field == "(root)" {
			problems = append(problems, e.Description())
			continue
		}
		problems = append(problems, fmt.Sprintf("%s: %s", field, e.Description()))
	}
	sort.Strings(problems)
	return &ValidationError{Problems: problems}
}

// toJSONValue converts a YAML-decoded value into something json.Marshal can
// handle (YAML permits non-string mapping keys; JSON Schema does not).
func toJSONValue(v interface{}) (interface{}, error) {
	switch t := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			conv, err := toJSONValue(val)
			if err != nil {
				return nil, err
			}
			out[k] = conv
		}
		return out, nil
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			ks, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("invalid prompt file: mapping key %v is not a string", k)
			}
			conv, err := toJSONValue(val)
			if err != nil {
				return nil, err
			}
			out[ks] = conv
		}
		return out, nil
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, val := range t {
			conv, err := toJSONValue(val)
			if err != nil {
				return nil, err
			}
			out[i] = conv
		}
		return out, nil
	default:
		return v, nil
	}
}
