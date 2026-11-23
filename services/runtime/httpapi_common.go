package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Shared HTTP-surface helpers. These are used by both the Bifrost-backed
// gateway (the default path) and the legacy Delos translators (compiled behind
// -tags=delos_translators), so they live untagged in one place.

// nowFunc is swappable in tests.
var nowFunc = timeNow

type requestIDKey struct{}

// RequestID returns the request ID assigned by the gateway middleware.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func timeNow() time.Time { return time.Now() }

// maxRequestBody bounds a single gateway request. Completions with large
// conversations and base64 images are legitimate, so this is generous; the
// limit exists so an unauthenticated client cannot stream an unbounded body
// into memory.
const maxRequestBody = 32 << 20

// limitBody caps the request body before any decoding happens.
func limitBody(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ---- error envelopes ----

// oaiErrorEnvelope is the OpenAI-style error envelope.
type oaiErrorEnvelope struct {
	Error oaiErrorBody `json:"error"`
}

type oaiErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   string `json:"param,omitempty"`
	Code    string `json:"code,omitempty"`
}

func (s *HTTPServer) writeOpenAIError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	typ := "invalid_request_error"
	if status >= 500 {
		typ = "api_error"
	}
	if reqID := RequestID(r.Context()); reqID != "" {
		msg = msg + " (request id: " + reqID + ")"
	}
	s.tailFailure(r, status, code, msg)
	writeJSON(w, status, oaiErrorEnvelope{Error: oaiErrorBody{Message: msg, Type: typ, Code: code}})
}

// anthErrorEnvelope is the Anthropic-style error envelope.
type anthErrorEnvelope struct {
	Type  string        `json:"type"`
	Error anthErrorBody `json:"error"`
}

type anthErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (s *HTTPServer) writeAnthropicError(w http.ResponseWriter, r *http.Request, status int, typ, msg string) {
	if reqID := RequestID(r.Context()); reqID != "" {
		msg = msg + " (request id: " + reqID + ")"
	}
	s.tailFailure(r, status, typ, msg)
	writeJSON(w, status, anthErrorEnvelope{Type: "error", Error: anthErrorBody{Type: typ, Message: msg}})
}

// ---- shared helpers previously scattered across the surface files ----

// withKey attaches the authenticated virtual key to the request context.
// Handlers receive the key as a value, but the error-envelope writers do not:
// they recover it from the context to attribute the tail event to the tenant
// that made the request. Without this, every failed request belongs to nobody
// and is invisible to the one subscriber entitled to see it.
func withKey(r *http.Request, key *VirtualKey) *http.Request {
	if key == nil {
		return r
	}
	return r.WithContext(WithAuthedKey(r.Context(), key))
}

// keyAllowsModel reports whether a key may see or use a model. A nil key is
// dev mode (no virtual keys configured), which scopes nothing.
func keyAllowsModel(key *VirtualKey, model string) bool {
	return key == nil || key.AllowsModel(model)
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// modelObject is the OpenAI GET /v1/models wire shape.
type modelObject struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}
