package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/tracing"
	otelplugin "github.com/maximhq/bifrost/plugins/otel"
	"github.com/google/uuid"
)

// Bifrost-backed gateway HTTP server. This is the default path: thin net/http
// handlers over Bifrost core (Passthrough/PassthroughStream for same-provider
// calls, ListAllModels/ModelRetrieveRequest for the model catalog). The legacy
// Delos translators remain reachable in a -tags=delos_translators build via
// DELOS_GATEWAY_BIFROST=0.

// HTTPServer serves the OpenAI-compatible and Anthropic-compatible HTTP
// surfaces of the gateway. In the default build every /v1 route is served by
// Bifrost core; in a delos_translators build the same type also carries the
// legacy Delos handlers, selected by the DELOS_GATEWAY_BIFROST switch.
type HTTPServer struct {
	service *RuntimeService
	logger  *slog.Logger
	mux     *http.ServeMux

	auth           *KeyAuthenticator // nil = dev mode, no auth
	keyStore       KeyStore
	requestTimeout time.Duration
	tail           *TailBroadcaster // nil = live request streaming disabled

	// Bifrost path state. bifrost is nil only if Bifrost failed to initialise.
	bifrost       *bifrost.Bifrost
	bifrostLogger schemas.Logger
	bifrostOnce   sync.Once
	bifrostErr    error
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

// NewHTTPServer creates the gateway HTTP API. The served path honours the
// DELOS_GATEWAY_BIFROST runtime switch: the default (and every non-legacy
// build) is Bifrost; a delos_translators build with DELOS_GATEWAY_BIFROST=0
// serves the legacy Delos translators.
func NewHTTPServer(service *RuntimeService, logger *slog.Logger, opts ...HTTPServerOption) *HTTPServer {
	s := newHTTPServer(service, logger, opts...)
	if useBifrostPath() {
		s.registerBifrostRoutes()
	} else {
		registerLegacyRoutes(s)
	}
	s.mux.HandleFunc("GET /v1/events", s.handleTailEvents)
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	return s
}

// NewBifrostHTTPServer is the in-process Bifrost-path hook for the swap-parity
// suite: it always serves Bifrost regardless of DELOS_GATEWAY_BIFROST.
func NewBifrostHTTPServer(service *RuntimeService, logger *slog.Logger, opts ...HTTPServerOption) *HTTPServer {
	s := newHTTPServer(service, logger, opts...)
	s.registerBifrostRoutes()
	s.mux.HandleFunc("GET /v1/events", s.handleTailEvents)
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	return s
}

func newHTTPServer(service *RuntimeService, logger *slog.Logger, opts ...HTTPServerOption) *HTTPServer {
	s := &HTTPServer{
		service: service,
		logger:  logger.With("component", "httpapi"),
		mux:     http.NewServeMux(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// useBifrostPath reports whether this process serves requests through Bifrost.
// Bifrost is always the default; a delos_translators build may select the
// legacy translators by setting DELOS_GATEWAY_BIFROST=0/false/off.
func useBifrostPath() bool {
	if !legacyTranslatorsCompiled {
		return true
	}
	v := strings.ToLower(strings.TrimSpace(os.Getenv("DELOS_GATEWAY_BIFROST")))
	return v != "0" && v != "false" && v != "off"
}

// ---- shared middleware ----

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

	timeout := s.requestTimeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	s.mux.ServeHTTP(w, r.WithContext(ctx))
}

func (s *HTTPServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"providers": len(s.service.Registry().List()),
	})
}

// ---- Bifrost initialisation ----

// newBifrost builds the embedded Bifrost core plus the otel connector pointed
// at Delos observe (when DELOS_OTLP_ENDPOINT is set). It returns the core, the
// connector (nil when tracing is off) and the tracer that owns them.
func (s *HTTPServer) initBifrost(ctx context.Context) (*bifrost.Bifrost, error) {
	s.bifrostLogger = bifrost.NewDefaultLogger(schemas.LogLevelInfo)

	store := tracing.NewTraceStore(5*time.Minute, s.bifrostLogger)
	tr := tracing.NewTracer(store, nil, s.bifrostLogger)

	config := schemas.BifrostConfig{
		Account: bifrostAccount{},
		Logger:  s.bifrostLogger,
		Tracer:  tr,
	}

	// Gap 4: point Bifrost's otel connector at Delos observe (OTLP). With no
	// endpoint configured the connector is skipped entirely and Bifrost emits no
	// spans. Cost is skipped until a model catalog is wired (D1 keeps the Delos
	// pricing table for models Bifrost lacks; the catalog lands with the D2 key
	// governance store).
	if endpoint := otlpCollectorURL(); endpoint != "" {
		otelCfg := &otelplugin.Config{Profiles: []*otelplugin.Profile{{
			Enabled:      true,
			TracesEnabled: true,
			ServiceName:   "delos-gateway",
			CollectorURL:  schemas.NewSecretVar(endpoint),
			TraceType:     otelplugin.TraceTypeGenAIExtension,
			Protocol:      otelplugin.ProtocolHTTP,
			Insecure:      true,
		}}}
		otelConn, err := otelplugin.Init(ctx, otelCfg, s.bifrostLogger, nil, "delos-gateway")
		if err != nil {
			s.logger.Warn("bifrost otel connector failed to init; spans disabled", "error", err)
		} else {
			config.LLMPlugins = append(config.LLMPlugins, otelConn)
			tr.SetObservabilityPlugins([]schemas.ObservabilityPlugin{otelConn}, nil)
		}
	}

	b, err := bifrost.Init(ctx, config)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// otlpCollectorURL derives the otel connector's full trace endpoint from
// DELOS_OTLP_ENDPOINT / DELOS_OTLP_PROTOCOL, the same knobs the Delos telemetry
// stack reads. The otel connector POSTs to the URL verbatim, so the /v1/traces
// path is appended here (the OTel SDK exporters do this themselves; Bifrost's
// connector does not).
func otlpCollectorURL() string {
	endpoint := strings.TrimSpace(os.Getenv("DELOS_OTLP_ENDPOINT"))
	if endpoint == "" {
		return ""
	}
	proto := strings.ToLower(strings.TrimSpace(os.Getenv("DELOS_OTLP_PROTOCOL")))
	if proto == "grpc" {
		return endpoint // gRPC target host:port; connector adds nothing
	}
	endpoint = strings.TrimSuffix(endpoint, "/")
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return endpoint + "/v1/traces"
	}
	return "http://" + endpoint + "/v1/traces"
}

// ---- Bifrost route registration ----

func (s *HTTPServer) registerBifrostRoutes() {
	s.mux.HandleFunc("POST /v1/messages", s.handleBifrostMessages)
	s.mux.HandleFunc("POST /v1/messages/count_tokens", s.handleBifrostCountTokens)
	s.mux.HandleFunc("POST /v1/responses", s.handleBifrostResponses)
	s.mux.HandleFunc("POST /v1/chat/completions", s.handleBifrostChatCompletions)
	s.mux.HandleFunc("POST /v1/embeddings", s.handleBifrostEmbeddings)
	s.mux.HandleFunc("GET /v1/models", s.handleBifrostListModels)
	s.mux.HandleFunc("GET /v1/models/{model}", s.handleBifrostGetModel)
}

// ensureBifrost lazily initialises the embedded Bifrost core on first use so
// that NewHTTPServer stays cheap (and a gateway with no traffic never spins up
// Bifrost). The core is built once and shared.
func (s *HTTPServer) ensureBifrost() *bifrost.Bifrost {
	s.bifrostOnce.Do(func() {
		s.bifrost, s.bifrostErr = s.initBifrost(context.Background())
	})
	if s.bifrostErr != nil {
		s.logger.Error("bifrost init failed", "error", s.bifrostErr)
		return nil
	}
	return s.bifrost
}

// newBifrostContext builds a request-scoped Bifrost context and stamps the
// session id (gap 5 seam: D2 supplies the key->session mapping; until then the
// session id is taken from a request header) so Bifrost's otel connector emits
// session.id on the root span.
func (s *HTTPServer) newBifrostContext(r *http.Request) *schemas.BifrostContext {
	timeout := s.requestTimeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx := schemas.NewBifrostContext(r.Context(), time.Now().Add(timeout))
	sid := r.Header.Get("x-delos-session")
	if sid == "" {
		sid = r.Header.Get("x-bf-session-id")
	}
	if sid == "" {
		sid = r.Header.Get("x-session-id")
	}
	if sid != "" {
		ctx.SetValue(schemas.BifrostContextKeySessionID, sid)
	}
	if rid := RequestID(r.Context()); rid != "" {
		ctx.SetValue(schemas.BifrostContextKeyRequestID, rid)
	}
	return ctx
}

// ---- handlers ----

func (s *HTTPServer) handleBifrostMessages(w http.ResponseWriter, r *http.Request) {
	key, ok := s.authenticate(w, r, true)
	if !ok {
		return
	}
	r = withKey(r, key)
	limitBody(w, r)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeAnthropicError(w, r, http.StatusBadRequest, "invalid_request_error", "could not read request body: "+err.Error())
		return
	}
	model := bodyModel(body)
	s.tailModel(r.Context(), model)
	if be := s.checkKeyLimits(r.Context(), key, model); be != nil {
		s.writeBudgetError(w, r, be, true)
		return
	}

	// Same-provider (Anthropic->Anthropic) is byte-faithful passthrough: the
	// body and anthropic-* headers go upstream exactly as sent. Cross-provider
	// requests are handled by the legacy translator where it is compiled.
	if providerForModel(model, "/v1/messages") != schemas.Anthropic {
		s.serveCrossProvider(w, r, body)
		return
	}
	s.passthrough(w, r, schemas.Anthropic, body, true, key)
}

func (s *HTTPServer) handleBifrostCountTokens(w http.ResponseWriter, r *http.Request) {
	key, ok := s.authenticate(w, r, true)
	if !ok {
		return
	}
	r = withKey(r, key)
	limitBody(w, r)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeAnthropicError(w, r, http.StatusBadRequest, "invalid_request_error", "could not read request body: "+err.Error())
		return
	}
	model := bodyModel(body)
	s.tailModel(r.Context(), model)
	if be := s.checkKeyLimits(r.Context(), key, model); be != nil {
		s.writeBudgetError(w, r, be, true)
		return
	}
	// Byte-faithful same-provider count_tokens via Bifrost passthrough. The
	// note 0001 table says "via CountTokensRequest"; that path needs an
	// Anthropic->Responses converter the SDK does not expose, so D1 uses
	// passthrough (identical wire result for the same-provider case) and the
	// CountTokensRequest path lands with the cross-provider translator.
	s.passthrough(w, r, schemas.Anthropic, body, true, key)
}

func (s *HTTPServer) handleBifrostResponses(w http.ResponseWriter, r *http.Request) {
	key, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	r = withKey(r, key)
	limitBody(w, r)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeOpenAIError(w, r, http.StatusBadRequest, "invalid_json", "could not read request body: "+err.Error())
		return
	}
	model := bodyModel(body)
	s.tailModel(r.Context(), model)
	if be := s.checkKeyLimits(r.Context(), key, model); be != nil {
		s.writeBudgetError(w, r, be, false)
		return
	}
	// Codex uses the OpenAI Responses API: passthrough for OpenAI.
	if providerForModel(model, "/v1/responses") != schemas.OpenAI {
		s.serveCrossProvider(w, r, body)
		return
	}
	s.passthrough(w, r, schemas.OpenAI, body, false, key)
}

func (s *HTTPServer) handleBifrostChatCompletions(w http.ResponseWriter, r *http.Request) {
	key, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	r = withKey(r, key)
	limitBody(w, r)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeOpenAIError(w, r, http.StatusBadRequest, "invalid_json", "could not read request body: "+err.Error())
		return
	}
	model := bodyModel(body)
	s.tailModel(r.Context(), model)
	if be := s.checkKeyLimits(r.Context(), key, model); be != nil {
		s.writeBudgetError(w, r, be, false)
		return
	}
	if providerForModel(model, "/v1/chat/completions") != schemas.OpenAI {
		s.serveCrossProvider(w, r, body)
		return
	}
	s.passthrough(w, r, schemas.OpenAI, body, false, key)
}

func (s *HTTPServer) handleBifrostEmbeddings(w http.ResponseWriter, r *http.Request) {
	key, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	r = withKey(r, key)
	limitBody(w, r)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeOpenAIError(w, r, http.StatusBadRequest, "invalid_json", "could not read request body: "+err.Error())
		return
	}
	model := bodyModel(body)
	s.tailModel(r.Context(), model)
	if be := s.checkKeyLimits(r.Context(), key, model); be != nil {
		s.writeBudgetError(w, r, be, false)
		return
	}
	if providerForModel(model, "/v1/embeddings") != schemas.OpenAI {
		s.serveCrossProvider(w, r, body)
		return
	}
	s.passthrough(w, r, schemas.OpenAI, body, false, key)
}

func (s *HTTPServer) handleBifrostListModels(w http.ResponseWriter, r *http.Request) {
	key, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	r = withKey(r, key)
	b := s.ensureBifrost()
	if b == nil {
		s.writeOpenAIError(w, r, http.StatusServiceUnavailable, "gateway_unavailable", "bifrost core is unavailable")
		return
	}
	ctx := s.newBifrostContext(r)
	defer ctx.Cancel()
	resp, bErr := b.ListAllModels(ctx, &schemas.BifrostListModelsRequest{})
	if bErr != nil {
		s.writeBifrostError(w, r, bErr, false)
		return
	}
	models := make([]modelObject, 0, len(resp.Data))
	for _, m := range resp.Data {
		id := m.ID
		if !keyAllowsModel(key, id) {
			continue
		}
		mo := modelObject{ID: id, Object: "model", OwnedBy: deref(m.OwnedBy)}
		if m.Created != nil {
			mo.Created = *m.Created
		}
		models = append(models, mo)
	}
	if models == nil {
		models = []modelObject{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": models})
}

func (s *HTTPServer) handleBifrostGetModel(w http.ResponseWriter, r *http.Request) {
	key, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	r = withKey(r, key)
	want := r.PathValue("model")
	if !keyAllowsModel(key, want) {
		s.writeOpenAIError(w, r, http.StatusNotFound, "model_not_found", fmt.Sprintf("model %q does not exist", want))
		return
	}
	provider, model := resolveProviderForModel(want)
	if provider == "" {
		s.writeOpenAIError(w, r, http.StatusNotFound, "model_not_found", fmt.Sprintf("model %q does not exist", want))
		return
	}
	b := s.ensureBifrost()
	if b == nil {
		s.writeOpenAIError(w, r, http.StatusServiceUnavailable, "gateway_unavailable", "bifrost core is unavailable")
		return
	}
	ctx := s.newBifrostContext(r)
	defer ctx.Cancel()
	resp, bErr := b.ModelRetrieveRequest(ctx, &schemas.BifrostModelRetrieveRequest{Provider: provider, Model: model})
	if bErr != nil {
		s.writeBifrostError(w, r, bErr, false)
		return
	}
	mo := modelObject{ID: resp.ID, Object: "model", OwnedBy: deref(resp.OwnedBy)}
	if resp.Created != nil {
		mo.Created = *resp.Created
	}
	writeJSON(w, http.StatusOK, mo)
}

// serveCrossProvider hands a cross-provider request to the legacy Delos
// translator where it is compiled; in a Bifrost-only build it is a clean 501.
func (s *HTTPServer) serveCrossProvider(w http.ResponseWriter, r *http.Request, body []byte) {
	if s.serveLegacyRoute(w, r, body) {
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v1/messages") {
		s.writeAnthropicError(w, r, http.StatusNotImplemented, "invalid_request_error",
			"cross-provider /v1/messages translation is not available in this build (rebuild with -tags=delos_translators)")
		return
	}
	s.writeOpenAIError(w, r, http.StatusNotImplemented, "unsupported_route",
		"cross-provider translation is not available in this build (rebuild with -tags=delos_translators)")
}

// ---- passthrough machinery ----

// passthrough forwards the raw request body and headers to the provider through
// Bifrost core and streams the provider bytes back unchanged. key is the
// authenticated Delos virtual key (nil in dev mode) whose budget the completed
// request is metered against.
func (s *HTTPServer) passthrough(w http.ResponseWriter, r *http.Request, provider schemas.ModelProvider, body []byte, anthSurface bool, key *VirtualKey) {
	b := s.ensureBifrost()
	if b == nil {
		if anthSurface {
			s.writeAnthropicError(w, r, http.StatusServiceUnavailable, "api_error", "bifrost core is unavailable")
		} else {
			s.writeOpenAIError(w, r, http.StatusServiceUnavailable, "gateway_unavailable", "bifrost core is unavailable")
		}
		return
	}
	ctx := s.newBifrostContext(r)
	defer ctx.Cancel()

	req := &schemas.BifrostPassthroughRequest{
		Method:      r.Method,
		Path:        r.URL.Path,
		RawQuery:    r.URL.RawQuery,
		Body:        body,
		SafeHeaders: passthroughHeaders(r.Header),
		Model:       bodyModel(body),
	}

	start := nowFunc()
	streams := bodyStreams(body)
	if !streams {
		resp, bErr := b.Passthrough(ctx, provider, req)
		if bErr != nil {
			s.writeBifrostError(w, r, bErr, anthSurface)
			s.tailFailure(r, bErr.EffectiveHTTPStatus(), "", bErr.GetErrorString())
			return
		}
		if resp.PassthroughUsage != nil {
			s.recordUsage(context.WithoutCancel(r.Context()), key, delosUsage(resp.PassthroughUsage.LLMUsage))
		}
		s.tailPassthrough(r, provider, resp, start)
		writeBifrostResponse(w, resp.StatusCode, resp.Headers, resp.Body)
		return
	}

	stream, bErr := b.PassthroughStream(ctx, provider, req)
	if bErr != nil {
		s.writeBifrostError(w, r, bErr, anthSurface)
		s.tailFailure(r, bErr.EffectiveHTTPStatus(), "", bErr.GetErrorString())
		return
	}

	first, ok := <-stream
	if !ok || first == nil {
		http.Error(w, "passthrough stream ended before headers", http.StatusBadGateway)
		return
	}
	if first.BifrostError != nil {
		s.writeBifrostError(w, r, first.BifrostError, anthSurface)
		return
	}
	resp := first.BifrostPassthroughResponse
	if resp == nil {
		http.Error(w, "passthrough stream returned empty first chunk", http.StatusBadGateway)
		return
	}
	var lastUsage *schemas.BifrostPassthroughUsage

	ct := headerValue(resp.Headers, "content-type")
	if ct == "" {
		ct = "text/event-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-cache")
	copyResponseHeaders(w, resp.Headers, true)
	w.WriteHeader(resp.StatusCode)
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	if len(resp.Body) > 0 {
		_, _ = w.Write(resp.Body)
		flusher.Flush()
	}
	for chunk := range stream {
		if chunk == nil {
			continue
		}
		if chunk.BifrostError != nil {
			s.tailFailure(r, chunk.BifrostError.EffectiveHTTPStatus(), "", chunk.BifrostError.GetErrorString())
			return
		}
		if p := chunk.BifrostPassthroughResponse; p != nil {
			if p.PassthroughUsage != nil {
				lastUsage = p.PassthroughUsage
			}
			if len(p.Body) > 0 {
				if _, err := w.Write(p.Body); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
	if lastUsage != nil {
		s.recordUsage(context.WithoutCancel(r.Context()), key, delosUsage(lastUsage.LLMUsage))
	}
	s.tailPassthrough(r, provider, resp, start)
}

// tailPassthrough publishes the one per-request tail event for a passthrough
// response. Usage is extracted from the passthrough response's usage block.
func (s *HTTPServer) tailPassthrough(r *http.Request, provider schemas.ModelProvider, resp *schemas.BifrostPassthroughResponse, start time.Time) {
	if s.tail == nil {
		return
	}
	ev := TailEvent{
		Model:    bodyModelFrom(r),
		Provider: string(provider),
		Status:   resp.StatusCode,
	}
	if resp.PassthroughUsage != nil && resp.PassthroughUsage.LLMUsage != nil {
		u := resp.PassthroughUsage.LLMUsage
		ev.PromptTokens = u.PromptTokens
		ev.CompletionTokens = u.CompletionTokens
		if u.Cost != nil {
			ev.CostUSD = u.Cost.TotalCost
		}
	}
	if key := AuthedKey(r.Context()); key != nil {
		ev.KeyName = key.Name
		ev.KeyID = key.ID
	}
	s.tailEmit(r, ev)
}

// bodyModelFrom reads the model from the request body, or the recorded tail
// model when the body is gone.
func bodyModelFrom(r *http.Request) string {
	if st := tailStateFrom(r.Context()); st != nil {
		st.mu.Lock()
		m := st.model
		st.mu.Unlock()
		if m != "" {
			return m
		}
	}
	return ""
}

// ---- helpers ----

// bodyModel extracts "model" from a JSON request body; empty is fine.
func bodyModel(body []byte) string {
	var m struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &m)
	return m.Model
}

// bodyStreams reports whether the request body asks for streaming.
func bodyStreams(body []byte) bool {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return false
	}
	if v, ok := m["stream"].(bool); ok {
		return v
	}
	return false
}

// providerForModel resolves the provider the surface's same-provider path
// serves, or "" when the model belongs to another provider (cross-provider).
func providerForModel(model, path string) schemas.ModelProvider {
	name := wellKnownProvider(model)
	if i := strings.IndexByte(model, '/'); i >= 0 {
		name = model[:i]
	}
	switch path {
	case "/v1/messages", "/v1/messages/count_tokens":
		if name == "anthropic" {
			return schemas.Anthropic
		}
	default:
		if name == "openai" {
			return schemas.OpenAI
		}
	}
	return ""
}

// resolveProviderForModel maps a model name (or "provider/model") to a Bifrost
// provider key and the model to send.
func resolveProviderForModel(model string) (schemas.ModelProvider, string) {
	if name, rest, ok := cutProviderPrefix(model); ok {
		if p := bifrostProvider(name); p != "" {
			return p, rest
		}
	}
	if name := wellKnownProvider(model); name != "" {
		if p := bifrostProvider(name); p != "" {
			return p, model
		}
	}
	return "", model
}

// bifrostProvider maps a Delos provider name to a Bifrost ModelProvider.
func bifrostProvider(name string) schemas.ModelProvider {
	switch name {
	case "openai":
		return schemas.OpenAI
	case "anthropic":
		return schemas.Anthropic
	case "gemini":
		return schemas.Gemini
	case "bedrock":
		return schemas.Bedrock
	case "ollama":
		return schemas.Ollama
	}
	return ""
}

// passthroughHeaders copies request headers for Bifrost to forward, stripping
// Delos auth and hop-by-hop headers the provider must not receive. anthropic-*
// headers are preserved exactly as sent.
func passthroughHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vs := range h {
		switch strings.ToLower(k) {
		case "authorization", "x-api-key", "host", "content-length",
			"transfer-encoding", "connection", "accept-encoding":
			continue
		}
		if len(vs) > 0 {
			out[k] = vs[0]
		}
	}
	return out
}

func headerValue(headers map[string]string, key string) string {
	for k, v := range headers {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

func copyResponseHeaders(w http.ResponseWriter, headers map[string]string, streaming bool) {
	for k, v := range headers {
		switch strings.ToLower(k) {
		case "connection", "transfer-encoding", "content-length", "content-type":
			continue
		case "cache-control":
			if streaming {
				continue
			}
		}
		w.Header().Set(k, v)
	}
}

func writeBifrostResponse(w http.ResponseWriter, status int, headers map[string]string, body []byte) {
	ct := headerValue(headers, "content-type")
	if ct == "" {
		ct = "application/json"
	}
	w.Header().Set("Content-Type", ct)
	copyResponseHeaders(w, headers, false)
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeBifrostError maps a Bifrost error onto the surface's error envelope.
func (s *HTTPServer) writeBifrostError(w http.ResponseWriter, r *http.Request, bErr *schemas.BifrostError, anthSurface bool) {
	status := bErr.EffectiveHTTPStatus()
	msg := bErr.GetErrorString()
	if anthSurface {
		typ := "api_error"
		if status < 500 {
			typ = "invalid_request_error"
		}
		s.writeAnthropicError(w, r, status, typ, msg)
		return
	}
	code := "provider_error"
	if status < 500 {
		code = "invalid_request"
	}
	s.writeOpenAIError(w, r, status, code, msg)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// writeBudgetError maps a Delos key budget/scope refusal onto the surface's
// error envelope. Keys stay on the Delos key store through D1; D2 moves them
// onto Bifrost governance and replaces this gate.
func (s *HTTPServer) writeBudgetError(w http.ResponseWriter, r *http.Request, be *budgetError, anthSurface bool) {
	if anthSurface {
		typ := "rate_limit_error"
		if be.code == "model_not_allowed" {
			typ = "permission_error"
		}
		s.writeAnthropicError(w, r, budgetStatus(be), typ, be.msg)
		return
	}
	s.writeOpenAIError(w, r, budgetStatus(be), be.code, be.msg)
}

// delosUsage converts a Bifrost usage block into the Delos Usage used by key
// metering and the tail feed. Cache read/write tokens are surfaced here, which
// is gap 2: they were previously parsed and dropped by the Delos translators.
func delosUsage(u *schemas.BifrostLLMUsage) Usage {
	if u == nil {
		return Usage{}
	}
	us := Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
	}
	if u.Cost != nil {
		us.CostUSD = u.Cost.TotalCost
	}
	if u.PromptTokensDetails != nil {
		us.CacheReadTokens = u.PromptTokensDetails.CachedReadTokens
		us.CacheCreationTokens = u.PromptTokensDetails.CachedWriteTokens
	}
	return us
}
