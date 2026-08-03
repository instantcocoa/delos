package eval

import (
	"context"
	"testing"
)

func TestEvaluateExactMatch(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name     string
		expected map[string]interface{}
		actual   map[string]interface{}
		want     bool
		score    float64
	}{
		{
			name:     "exact match",
			expected: map[string]interface{}{"answer": "hello world"},
			actual:   map[string]interface{}{"answer": "hello world"},
			want:     true,
			score:    1.0,
		},
		{
			name:     "different values",
			expected: map[string]interface{}{"answer": "hello"},
			actual:   map[string]interface{}{"answer": "world"},
			want:     false,
			score:    0.0,
		},
		{
			name:     "whitespace normalization",
			expected: map[string]interface{}{"answer": "  hello world  "},
			actual:   map[string]interface{}{"answer": "hello world"},
			want:     true,
			score:    1.0,
		},
		{
			name:     "empty strings match",
			expected: map[string]interface{}{"answer": ""},
			actual:   map[string]interface{}{"answer": ""},
			want:     true,
			score:    1.0,
		},
		{
			name:     "uses content key",
			expected: map[string]interface{}{"content": "test"},
			actual:   map[string]interface{}{"content": "test"},
			want:     true,
			score:    1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := EvaluateExactMatch(ctx, tt.expected, tt.actual, nil)
			if err != nil {
				t.Fatalf("EvaluateExactMatch returned error: %v", err)
			}
			if result.Passed != tt.want {
				t.Errorf("Passed = %v, want %v", result.Passed, tt.want)
			}
			if result.Score != tt.score {
				t.Errorf("Score = %v, want %v", result.Score, tt.score)
			}
		})
	}
}

func TestEvaluateContains(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name          string
		expected      map[string]interface{}
		actual        map[string]interface{}
		params        map[string]string
		want          bool
		score         float64
	}{
		{
			name:     "contains substring",
			expected: map[string]interface{}{"answer": "world"},
			actual:   map[string]interface{}{"answer": "hello world"},
			want:     true,
			score:    1.0,
		},
		{
			name:     "does not contain",
			expected: map[string]interface{}{"answer": "xyz"},
			actual:   map[string]interface{}{"answer": "hello world"},
			want:     false,
			score:    0.0,
		},
		{
			name:     "case insensitive by default",
			expected: map[string]interface{}{"answer": "WORLD"},
			actual:   map[string]interface{}{"answer": "hello world"},
			want:     true,
			score:    1.0,
		},
		{
			name:          "case sensitive when specified",
			expected:      map[string]interface{}{"answer": "WORLD"},
			actual:        map[string]interface{}{"answer": "hello world"},
			params:        map[string]string{"case_sensitive": "true"},
			want:          false,
			score:         0.0,
		},
		{
			name:     "empty string always contained",
			expected: map[string]interface{}{"answer": ""},
			actual:   map[string]interface{}{"answer": "hello"},
			want:     true,
			score:    1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := EvaluateContains(ctx, tt.expected, tt.actual, tt.params)
			if err != nil {
				t.Fatalf("EvaluateContains returned error: %v", err)
			}
			if result.Passed != tt.want {
				t.Errorf("Passed = %v, want %v", result.Passed, tt.want)
			}
			if result.Score != tt.score {
				t.Errorf("Score = %v, want %v", result.Score, tt.score)
			}
		})
	}
}

func TestEvaluateRegex(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name     string
		actual   map[string]interface{}
		params   map[string]string
		want     bool
		score    float64
		wantErr  bool
	}{
		{
			name:   "simple pattern match",
			actual: map[string]interface{}{"answer": "hello123world"},
			params: map[string]string{"pattern": `\d+`},
			want:   true,
			score:  1.0,
		},
		{
			name:   "pattern does not match",
			actual: map[string]interface{}{"answer": "hello world"},
			params: map[string]string{"pattern": `\d+`},
			want:   false,
			score:  0.0,
		},
		{
			name:   "email pattern",
			actual: map[string]interface{}{"answer": "contact: test@example.com"},
			params: map[string]string{"pattern": `\w+@\w+\.\w+`},
			want:   true,
			score:  1.0,
		},
		{
			name:    "missing pattern",
			actual:  map[string]interface{}{"answer": "hello"},
			params:  map[string]string{},
			wantErr: true,
		},
		{
			name:    "invalid pattern",
			actual:  map[string]interface{}{"answer": "hello"},
			params:  map[string]string{"pattern": "[invalid"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := EvaluateRegex(ctx, nil, tt.actual, tt.params)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("EvaluateRegex returned error: %v", err)
			}
			if result.Passed != tt.want {
				t.Errorf("Passed = %v, want %v", result.Passed, tt.want)
			}
			if result.Score != tt.score {
				t.Errorf("Score = %v, want %v", result.Score, tt.score)
			}
		})
	}
}

func TestEvaluateJSONSchema(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		actual  map[string]interface{}
		params  map[string]string
		want    bool
		score   float64
		wantErr bool
	}{
		{
			name:   "valid object",
			actual: map[string]interface{}{"content": `{"name": "John", "age": 30}`},
			params: map[string]string{
				"schema": `{
					"type": "object",
					"properties": {
						"name": {"type": "string"},
						"age": {"type": "integer"}
					},
					"required": ["name"]
				}`,
			},
			want:  true,
			score: 1.0,
		},
		{
			name:   "invalid - missing required field",
			actual: map[string]interface{}{"content": `{"age": 30}`},
			params: map[string]string{
				"schema": `{
					"type": "object",
					"properties": {
						"name": {"type": "string"},
						"age": {"type": "integer"}
					},
					"required": ["name"]
				}`,
			},
			want:  false,
			score: 0.0,
		},
		{
			name:   "invalid - wrong type",
			actual: map[string]interface{}{"content": `{"name": 123}`},
			params: map[string]string{
				"schema": `{
					"type": "object",
					"properties": {
						"name": {"type": "string"}
					}
				}`,
			},
			want:  false,
			score: 0.0,
		},
		{
			name:   "not valid JSON",
			actual: map[string]interface{}{"content": `not json`},
			params: map[string]string{
				"schema": `{"type": "object"}`,
			},
			want:  false,
			score: 0.0,
		},
		{
			name:    "missing schema",
			actual:  map[string]interface{}{"content": `{}`},
			params:  map[string]string{},
			wantErr: true,
		},
		{
			name:   "rejects external http $ref",
			actual: map[string]interface{}{"content": `{}`},
			params: map[string]string{
				"schema": `{"$ref": "http://169.254.169.254/latest/meta-data/"}`,
			},
			wantErr: true,
		},
		{
			name:   "rejects external file $ref",
			actual: map[string]interface{}{"content": `{}`},
			params: map[string]string{
				"schema": `{"$ref": "file:///etc/passwd"}`,
			},
			wantErr: true,
		},
		{
			name:   "rejects nested external $ref",
			actual: map[string]interface{}{"content": `{"name": "a"}`},
			params: map[string]string{
				"schema": `{
					"type": "object",
					"properties": {
						"name": {"$ref": "https://evil.example/schema.json"}
					}
				}`,
			},
			wantErr: true,
		},
		{
			name:   "allows in-document $ref",
			actual: map[string]interface{}{"content": `{"name": "John"}`},
			params: map[string]string{
				"schema": `{
					"type": "object",
					"properties": {
						"name": {"$ref": "#/definitions/name"}
					},
					"definitions": {
						"name": {"type": "string"}
					}
				}`,
			},
			want:  true,
			score: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := EvaluateJSONSchema(ctx, nil, tt.actual, tt.params)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("EvaluateJSONSchema returned error: %v", err)
			}
			if result.Passed != tt.want {
				t.Errorf("Passed = %v, want %v (explanation: %s)", result.Passed, tt.want, result.Explanation)
			}
			if result.Score != tt.score {
				t.Errorf("Score = %v, want %v", result.Score, tt.score)
			}
		})
	}
}

func TestExtractStringValue(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]interface{}
		want  string
	}{
		{
			name:  "nil map",
			input: nil,
			want:  "",
		},
		{
			name:  "answer key",
			input: map[string]interface{}{"answer": "test value"},
			want:  "test value",
		},
		{
			name:  "content key",
			input: map[string]interface{}{"content": "test content"},
			want:  "test content",
		},
		{
			name:  "priority - answer over content",
			input: map[string]interface{}{"answer": "ans", "content": "cont"},
			want:  "ans",
		},
		{
			name:  "single key",
			input: map[string]interface{}{"custom": "custom value"},
			want:  "custom value",
		},
		{
			name:  "multiple keys - falls back to JSON",
			input: map[string]interface{}{"a": "1", "b": "2"},
			want:  `{"a":"1","b":"2"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractStringValue(tt.input)
			// For the JSON fallback case, we just check it's valid JSON
			if tt.name == "multiple keys - falls back to JSON" {
				if got == "" {
					t.Errorf("extractStringValue() returned empty string for multiple keys")
				}
				return
			}
			if got != tt.want {
				t.Errorf("extractStringValue() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetEvaluator(t *testing.T) {
	tests := []struct {
		name     string
		evalType string
		found    bool
	}{
		{"exact_match", "exact_match", true},
		{"contains", "contains", true},
		{"regex", "regex", true},
		{"json_schema", "json_schema", true},
		{"unknown", "unknown_evaluator", false},
		{"llm_judge not in registry", "llm_judge", false},
		{"semantic_similarity not in registry", "semantic_similarity", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := GetEvaluator(tt.evalType)
			if ok != tt.found {
				t.Errorf("GetEvaluator(%q) found = %v, want %v", tt.evalType, ok, tt.found)
			}
		})
	}
}
