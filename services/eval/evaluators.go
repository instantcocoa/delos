package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/xeipuuv/gojsonschema"
)

// EvaluatorFunc is a function that evaluates actual output against expected output.
// Returns a score from 0 to 1 and optional explanation.
type EvaluatorFunc func(ctx context.Context, expected, actual map[string]interface{}, params map[string]string) (*EvaluatorResult, error)

// EvaluatorRegistry maps evaluator types to their implementations.
var EvaluatorRegistry = map[string]EvaluatorFunc{
	"exact_match": EvaluateExactMatch,
	"contains":    EvaluateContains,
	"regex":       EvaluateRegex,
	"json_schema": EvaluateJSONSchema,
	// llm_judge and semantic_similarity require runtime client - handled separately
}

// GetEvaluator returns an evaluator function by type.
func GetEvaluator(evalType string) (EvaluatorFunc, bool) {
	fn, ok := EvaluatorRegistry[evalType]
	return fn, ok
}

// EvaluateExactMatch checks if actual output exactly matches expected output.
func EvaluateExactMatch(ctx context.Context, expected, actual map[string]interface{}, params map[string]string) (*EvaluatorResult, error) {
	expectedStr := extractStringValue(expected)
	actualStr := extractStringValue(actual)

	// Normalize whitespace
	expectedStr = strings.TrimSpace(expectedStr)
	actualStr = strings.TrimSpace(actualStr)

	if expectedStr == actualStr {
		return &EvaluatorResult{
			EvaluatorType: "exact_match",
			Score:         1.0,
			Passed:        true,
			Explanation:   "Output exactly matches expected",
			Details: map[string]string{
				"expected": expectedStr,
				"actual":   actualStr,
			},
		}, nil
	}

	return &EvaluatorResult{
		EvaluatorType: "exact_match",
		Score:         0.0,
		Passed:        false,
		Explanation:   "Output does not match expected",
		Details: map[string]string{
			"expected": expectedStr,
			"actual":   actualStr,
		},
	}, nil
}

// EvaluateContains checks if actual output contains expected string(s).
func EvaluateContains(ctx context.Context, expected, actual map[string]interface{}, params map[string]string) (*EvaluatorResult, error) {
	expectedStr := extractStringValue(expected)
	actualStr := extractStringValue(actual)

	caseSensitive := params["case_sensitive"] == "true"

	if !caseSensitive {
		expectedStr = strings.ToLower(expectedStr)
		actualStr = strings.ToLower(actualStr)
	}

	if strings.Contains(actualStr, expectedStr) {
		return &EvaluatorResult{
			EvaluatorType: "contains",
			Score:         1.0,
			Passed:        true,
			Explanation:   "Output contains expected substring",
			Details: map[string]string{
				"expected":       extractStringValue(expected),
				"found_in":       extractStringValue(actual),
				"case_sensitive": fmt.Sprintf("%v", caseSensitive),
			},
		}, nil
	}

	return &EvaluatorResult{
		EvaluatorType: "contains",
		Score:         0.0,
		Passed:        false,
		Explanation:   "Output does not contain expected substring",
		Details: map[string]string{
			"expected":       extractStringValue(expected),
			"actual":         extractStringValue(actual),
			"case_sensitive": fmt.Sprintf("%v", caseSensitive),
		},
	}, nil
}

// EvaluateRegex checks if actual output matches a regex pattern.
func EvaluateRegex(ctx context.Context, expected, actual map[string]interface{}, params map[string]string) (*EvaluatorResult, error) {
	pattern := params["pattern"]
	if pattern == "" {
		return nil, fmt.Errorf("regex evaluator requires 'pattern' parameter")
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex pattern %q: %w", pattern, err)
	}

	actualStr := extractStringValue(actual)

	if re.MatchString(actualStr) {
		matches := re.FindAllString(actualStr, -1)
		return &EvaluatorResult{
			EvaluatorType: "regex",
			Score:         1.0,
			Passed:        true,
			Explanation:   fmt.Sprintf("Pattern matched %d time(s)", len(matches)),
			Details: map[string]string{
				"pattern": pattern,
				"matches": strings.Join(matches, ", "),
			},
		}, nil
	}

	return &EvaluatorResult{
		EvaluatorType: "regex",
		Score:         0.0,
		Passed:        false,
		Explanation:   "Pattern did not match",
		Details: map[string]string{
			"pattern": pattern,
			"actual":  actualStr,
		},
	}, nil
}

// EvaluateJSONSchema validates actual output against a JSON schema.
func EvaluateJSONSchema(ctx context.Context, expected, actual map[string]interface{}, params map[string]string) (*EvaluatorResult, error) {
	schemaStr := params["schema"]
	if schemaStr == "" {
		return nil, fmt.Errorf("json_schema evaluator requires 'schema' parameter")
	}

	// Reject schemas with "$ref" pointing outside the document. gojsonschema resolves
	// absolute-URI refs by fetching them (http(s):// over the network, file:// from disk),
	// which would let a caller-supplied schema trigger SSRF or local file reads.
	var schemaJSON interface{}
	if err := json.Unmarshal([]byte(schemaStr), &schemaJSON); err != nil {
		return nil, fmt.Errorf("invalid json_schema 'schema' parameter: %w", err)
	}
	if schemaHasExternalRef(schemaJSON) {
		return nil, fmt.Errorf("json_schema evaluator does not allow external $ref references")
	}

	actualStr := extractStringValue(actual)

	// Try to parse actual output as JSON
	var actualJSON interface{}
	if err := json.Unmarshal([]byte(actualStr), &actualJSON); err != nil {
		return &EvaluatorResult{
			EvaluatorType: "json_schema",
			Score:         0.0,
			Passed:        false,
			Explanation:   fmt.Sprintf("Output is not valid JSON: %v", err),
			Details: map[string]string{
				"error":  err.Error(),
				"actual": actualStr,
			},
		}, nil
	}

	// Validate against schema
	schemaLoader := gojsonschema.NewStringLoader(schemaStr)
	documentLoader := gojsonschema.NewGoLoader(actualJSON)

	result, err := gojsonschema.Validate(schemaLoader, documentLoader)
	if err != nil {
		return nil, fmt.Errorf("schema validation error: %w", err)
	}

	if result.Valid() {
		return &EvaluatorResult{
			EvaluatorType: "json_schema",
			Score:         1.0,
			Passed:        true,
			Explanation:   "Output matches JSON schema",
			Details: map[string]string{
				"schema": schemaStr,
			},
		}, nil
	}

	// Collect validation errors
	var errors []string
	for _, err := range result.Errors() {
		errors = append(errors, err.String())
	}

	return &EvaluatorResult{
		EvaluatorType: "json_schema",
		Score:         0.0,
		Passed:        false,
		Explanation:   fmt.Sprintf("Schema validation failed: %s", strings.Join(errors, "; ")),
		Details: map[string]string{
			"schema": schemaStr,
			"errors": strings.Join(errors, "\n"),
		},
	}, nil
}

// schemaHasExternalRef reports whether a parsed JSON schema document contains a "$ref"
// value that points outside the document (i.e. an absolute URI with a scheme, such as
// "http://", "https://", or "file://"), as opposed to an in-document pointer like "#/definitions/foo".
func schemaHasExternalRef(node interface{}) bool {
	switch v := node.(type) {
	case map[string]interface{}:
		for k, val := range v {
			if k == "$ref" {
				if s, ok := val.(string); ok && strings.Contains(s, "://") {
					return true
				}
			}
			if schemaHasExternalRef(val) {
				return true
			}
		}
	case []interface{}:
		for _, item := range v {
			if schemaHasExternalRef(item) {
				return true
			}
		}
	}
	return false
}

// extractStringValue extracts the primary string value from output.
// It handles common output formats like {"answer": "..."} or {"content": "..."}.
func extractStringValue(m map[string]interface{}) string {
	if m == nil {
		return ""
	}

	// Try common keys first
	for _, key := range []string{"answer", "content", "output", "text", "response", "result"} {
		if v, ok := m[key]; ok {
			return fmt.Sprintf("%v", v)
		}
	}

	// If only one key, use that value
	if len(m) == 1 {
		for _, v := range m {
			return fmt.Sprintf("%v", v)
		}
	}

	// Fall back to JSON representation
	b, _ := json.Marshal(m)
	return string(b)
}
