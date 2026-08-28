package runtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// nowFunc is swappable in tests.
var nowFunc = timeNow

// HTTPServer serves the OpenAI-compatible and Anthropic-compatible HTTP
// surfaces of the gateway. It is the primary (and only) API of the
// delos-gateway binary.
type HTTPServer struct {
	service        *RuntimeService
	logger         *slog.Logger
	mux            *http.ServeMux
	auth           *KeyAuthenticator // nil = dev mode, no auth
	keyStore       KeyStore
	requestTimeout time.Duration
	tail           *TailBroadcaster // nil = live request streaming disabled
}

// HTTPServerOption configures the gateway HTTP API.
type HTTPServerOption func(*HTTPServer)

// WithKeyStore enables virtual-key enforcement backed by the given store.
func WithKeyStore(store KeyStore) HTTPServerOption {
	return func(s *HTTPServer) {
		s.keyStore = store
		s.auth = NewKeyAuthenticator(store)
	}
}

// WithTailBroadcast enables GET /v1/events, the live request stream that
// backs `delos tail`.
func WithTailBroadcast(b *TailBroadcaster) HTTPServerOption {
	return func(s *HTTPServer) { s.tail = b }
}

// WithRequestTimeout sets the end-to-end budget for a single request,
// including all retries and fallbacks (default 5 minutes).
func WithRequestTimeout(d time.Duration) HTTPServerOption {
	return func(s *HTTPServer) { s.requestTimeout = d }
}

// NewHTTPServer creates the gateway HTTP API around a RuntimeService.
func NewHTTPServer(service *RuntimeService, logger *slog.Logger, opts ...HTTPServerOption) *HTTPServer {
	s := &HTTPServer{
		service: service,
		logger:  logger.With("component", "httpapi"),
		mux:     http.NewServeMux(),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.mux.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)
	s.mux.HandleFunc("POST /v1/embeddings", s.handleEmbeddings)
	s.mux.HandleFunc("GET /v1/models", s.handleListModels)
	s.mux.HandleFunc("GET /v1/models/{model}", s.handleGetModel)
	s.mux.HandleFunc("POST /v1/messages", s.handleAnthropicMessages)
	s.mux.HandleFunc("GET /v1/events", s.handleTailEvents)
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	return s
}

func (s *HTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if reqID == "" {
		reqID = "req_" + uuid.NewString()
	}
	w.Header().Set("X-Request-Id", reqID)
	ctx := context.WithValue(r.Context(), requestIDKey{}, reqID)
	if s.tail != nil {
		ctx = withTailState(ctx, nowFunc())
	}

	// End-to-end timeout budget covering retries and fallbacks.
	timeout := s.requestTimeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	s.mux.ServeHTTP(w, r.WithContext(ctx))
}

type requestIDKey struct{}

// RequestID returns the request ID assigned by the gateway middleware.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func (s *HTTPServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"providers": len(s.service.Registry().List()),
	})
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
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
