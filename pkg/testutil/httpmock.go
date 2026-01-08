package testutil

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
)

// MockHTTPClient provides a configurable mock HTTP client for testing.
type MockHTTPClient struct {
	mu           sync.Mutex
	responses    []MockResponse
	requests     []*http.Request
	requestBodies [][]byte
	defaultResponse *MockResponse
}

// MockResponse defines a mock HTTP response.
type MockResponse struct {
	StatusCode int
	Body       string
	Headers    map[string]string
	Error      error
	// Matcher optionally matches requests - if nil, matches all
	Matcher func(*http.Request) bool
}

// NewMockHTTPClient creates a new mock HTTP client.
func NewMockHTTPClient() *MockHTTPClient {
	return &MockHTTPClient{
		responses:     make([]MockResponse, 0),
		requests:      make([]*http.Request, 0),
		requestBodies: make([][]byte, 0),
	}
}

// AddResponse adds a mock response to the queue.
func (m *MockHTTPClient) AddResponse(resp MockResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses = append(m.responses, resp)
}

// SetDefaultResponse sets the default response when queue is empty.
func (m *MockHTTPClient) SetDefaultResponse(resp MockResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.defaultResponse = &resp
}

// Do implements the HTTP client interface.
func (m *MockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Store the request
	m.requests = append(m.requests, req)

	// Store request body if present
	if req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		m.requestBodies = append(m.requestBodies, body)
		req.Body = io.NopCloser(bytes.NewReader(body))
	} else {
		m.requestBodies = append(m.requestBodies, nil)
	}

	// Find matching response
	var resp *MockResponse
	for i, r := range m.responses {
		if r.Matcher == nil || r.Matcher(req) {
			resp = &m.responses[i]
			// Remove from queue
			m.responses = append(m.responses[:i], m.responses[i+1:]...)
			break
		}
	}

	if resp == nil {
		resp = m.defaultResponse
	}

	if resp == nil {
		return nil, &MockError{Message: "no mock response configured"}
	}

	if resp.Error != nil {
		return nil, resp.Error
	}

	httpResp := &http.Response{
		StatusCode: resp.StatusCode,
		Body:       io.NopCloser(strings.NewReader(resp.Body)),
		Header:     make(http.Header),
		Request:    req,
	}

	for k, v := range resp.Headers {
		httpResp.Header.Set(k, v)
	}

	return httpResp, nil
}

// Requests returns all captured requests.
func (m *MockHTTPClient) Requests() []*http.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requests
}

// RequestBodies returns all captured request bodies.
func (m *MockHTTPClient) RequestBodies() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requestBodies
}

// LastRequest returns the last captured request.
func (m *MockHTTPClient) LastRequest() *http.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.requests) == 0 {
		return nil
	}
	return m.requests[len(m.requests)-1]
}

// LastRequestBody returns the last captured request body.
func (m *MockHTTPClient) LastRequestBody() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.requestBodies) == 0 {
		return nil
	}
	return m.requestBodies[len(m.requestBodies)-1]
}

// Reset clears all captured requests and responses.
func (m *MockHTTPClient) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses = make([]MockResponse, 0)
	m.requests = make([]*http.Request, 0)
	m.requestBodies = make([][]byte, 0)
}

// MockError represents a mock error.
type MockError struct {
	Message string
}

func (e *MockError) Error() string {
	return e.Message
}

// Common mock response builders

// MockOpenAIResponse creates a mock OpenAI chat completion response.
func MockOpenAIResponse(content string) MockResponse {
	body, _ := json.Marshal(map[string]interface{}{
		"id":      "chatcmpl-test123",
		"object":  "chat.completion",
		"created": 1234567890,
		"model":   "gpt-4o",
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]string{
					"role":    "assistant",
					"content": content,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]int{
			"prompt_tokens":     10,
			"completion_tokens": 20,
			"total_tokens":      30,
		},
	})
	return MockResponse{
		StatusCode: 200,
		Body:       string(body),
		Headers:    map[string]string{"Content-Type": "application/json"},
	}
}

// MockAnthropicResponse creates a mock Anthropic message response.
func MockAnthropicResponse(content string) MockResponse {
	body, _ := json.Marshal(map[string]interface{}{
		"id":   "msg-test123",
		"type": "message",
		"role": "assistant",
		"content": []map[string]string{
			{
				"type": "text",
				"text": content,
			},
		},
		"model":         "claude-3-5-sonnet-20241022",
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage": map[string]int{
			"input_tokens":  10,
			"output_tokens": 20,
		},
	})
	return MockResponse{
		StatusCode: 200,
		Body:       string(body),
		Headers:    map[string]string{"Content-Type": "application/json"},
	}
}

// MockErrorResponse creates a mock error response.
func MockErrorResponse(statusCode int, message string) MockResponse {
	body, _ := json.Marshal(map[string]interface{}{
		"error": map[string]string{
			"message": message,
			"type":    "error",
		},
	})
	return MockResponse{
		StatusCode: statusCode,
		Body:       string(body),
		Headers:    map[string]string{"Content-Type": "application/json"},
	}
}

// MockTimeoutError creates a mock timeout error.
func MockTimeoutError() MockResponse {
	return MockResponse{
		Error: &MockError{Message: "context deadline exceeded"},
	}
}

// MockConnectionError creates a mock connection error.
func MockConnectionError() MockResponse {
	return MockResponse{
		Error: &MockError{Message: "connection refused"},
	}
}

// MockMalformedJSON creates a mock response with invalid JSON.
func MockMalformedJSON() MockResponse {
	return MockResponse{
		StatusCode: 200,
		Body:       `{"invalid json`,
		Headers:    map[string]string{"Content-Type": "application/json"},
	}
}

// MockEmptyResponse creates a mock empty response.
func MockEmptyResponse(statusCode int) MockResponse {
	return MockResponse{
		StatusCode: statusCode,
		Body:       "",
		Headers:    map[string]string{"Content-Type": "application/json"},
	}
}

// MockSSEStream creates a mock SSE streaming response.
func MockSSEStream(events []string) MockResponse {
	var builder strings.Builder
	for _, event := range events {
		builder.WriteString("data: ")
		builder.WriteString(event)
		builder.WriteString("\n\n")
	}
	builder.WriteString("data: [DONE]\n\n")
	return MockResponse{
		StatusCode: 200,
		Body:       builder.String(),
		Headers:    map[string]string{"Content-Type": "text/event-stream"},
	}
}
