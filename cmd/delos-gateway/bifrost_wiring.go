//go:build bifrost

// Bifrost spike wiring (D0). This is NOT production code: it is a thin
// net/http handler over Bifrost core's Passthrough / PassthroughStream that
// proves a request passes through Bifrost and streams back. It is compiled
// only with -tags=bifrost and is replaced by the D1 gateway work.
package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
)

// envAccount implements schemas.Account with keys and base URLs read from the
// environment, mirroring delos-gateway's own provider registration.
type envAccount struct{}

func (envAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	var providers []schemas.ModelProvider
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		providers = append(providers, schemas.Anthropic)
	}
	if os.Getenv("OPENAI_API_KEY") != "" {
		providers = append(providers, schemas.OpenAI)
	}
	return providers, nil
}

func (envAccount) GetKeysForProvider(_ context.Context, providerKey schemas.ModelProvider) ([]schemas.Key, error) {
	var v string
	switch providerKey {
	case schemas.Anthropic:
		v = os.Getenv("ANTHROPIC_API_KEY")
	case schemas.OpenAI:
		v = os.Getenv("OPENAI_API_KEY")
	}
	if v == "" {
		return nil, nil
	}
	return []schemas.Key{{
		ID:     string(providerKey) + "-env",
		Name:   string(providerKey) + " env key",
		Value:  *schemas.NewSecretVar(v),
		Models: schemas.WhiteList{"*"},
		Weight: 1,
	}}, nil
}

func baseURLFor(providerKey schemas.ModelProvider) string {
	switch providerKey {
	case schemas.Anthropic:
		if u := os.Getenv("DELOS_BIFROST_ANTHROPIC_BASE_URL"); u != "" {
			return strings.TrimSuffix(u, "/")
		}
		return "https://api.anthropic.com"
	case schemas.OpenAI:
		if u := os.Getenv("DELOS_BIFROST_OPENAI_BASE_URL"); u != "" {
			return strings.TrimSuffix(u, "/")
		}
		return "https://api.openai.com"
	}
	return ""
}

func (envAccount) GetConfigForProvider(providerKey schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	cfg := &schemas.ProviderConfig{
		NetworkConfig: schemas.NetworkConfig{BaseURL: baseURLFor(providerKey)},
	}
	cfg.CheckAndSetDefaults()
	return cfg, nil
}

// bifrostHandler builds the Bifrost passthrough handler, or returns nil when
// Bifrost cannot be initialized (e.g. no provider key configured).
func bifrostHandler() http.Handler {
	b, err := bifrost.Init(context.Background(), schemas.BifrostConfig{
		Account: envAccount{},
		Logger:  bifrost.NewDefaultLogger(schemas.LogLevelInfo),
	})
	if err != nil {
		return nil
	}
	return &bifrostPassthrough{b: b}
}

type bifrostPassthrough struct {
	b *bifrost.Bifrost
}

// ServeHTTP passes the raw request body and headers through Bifrost and
// streams the provider bytes back unchanged.
func (h *bifrostPassthrough) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	provider := providerForPath(r.URL.Path)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	safeHeaders := make(map[string]string, len(r.Header))
	for k, vs := range r.Header {
		if len(vs) > 0 {
			safeHeaders[k] = vs[0]
		}
	}

	req := &schemas.BifrostPassthroughRequest{
		Method:      r.Method,
		Path:        r.URL.Path,
		RawQuery:    r.URL.RawQuery,
		Body:        body,
		SafeHeaders: safeHeaders,
		Model:       modelFromBody(body),
	}

	ctx := schemas.NewBifrostContext(r.Context(), time.Now().Add(5*time.Minute))
	defer ctx.Cancel()

	if !streams(body) {
		resp, bErr := h.b.Passthrough(ctx, provider, req)
		if bErr != nil {
			writeBifrostError(w, bErr)
			return
		}
		writeBifrostResponse(w, resp.StatusCode, resp.Headers, resp.Body)
		return
	}

	stream, bErr := h.b.PassthroughStream(ctx, provider, req)
	if bErr != nil {
		writeBifrostError(w, bErr)
		return
	}

	first, ok := <-stream
	if !ok || first == nil {
		http.Error(w, "passthrough stream ended before headers", http.StatusBadGateway)
		return
	}
	if first.BifrostError != nil {
		writeBifrostError(w, first.BifrostError)
		return
	}
	resp := first.BifrostPassthroughResponse
	if resp == nil {
		http.Error(w, "passthrough stream returned empty first chunk", http.StatusBadGateway)
		return
	}

	ct := ""
	for k, v := range resp.Headers {
		if strings.EqualFold(k, "content-type") {
			ct = v
			break
		}
	}
	if ct == "" {
		ct = "text/event-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-cache")
	for k, v := range resp.Headers {
		switch strings.ToLower(k) {
		case "connection", "transfer-encoding", "content-length", "content-type", "cache-control":
			continue
		default:
			w.Header().Set(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	if len(resp.Body) > 0 {
		if _, err := w.Write(resp.Body); err != nil {
			return
		}
		flusher.Flush()
	}
	for chunk := range stream {
		if chunk == nil {
			continue
		}
		if chunk.BifrostError != nil {
			return
		}
		if p := chunk.BifrostPassthroughResponse; p != nil && len(p.Body) > 0 {
			if _, err := w.Write(p.Body); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func providerForPath(path string) schemas.ModelProvider {
	if strings.HasPrefix(path, "/v1/messages") {
		return schemas.Anthropic
	}
	return schemas.OpenAI
}

// streams reports whether the request body asks for streaming, matching the
// transport's passthrough behaviour for the two surfaces we proxy.
func streams(body []byte) bool {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return false
	}
	if v, ok := m["stream"].(bool); ok {
		return v
	}
	return false
}

// modelFromBody extracts "model" for key selection; empty is fine for
// passthrough.
func modelFromBody(body []byte) string {
	var m struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &m)
	return m.Model
}

func writeBifrostResponse(w http.ResponseWriter, status int, headers map[string]string, body []byte) {
	ct := ""
	for k, v := range headers {
		if strings.EqualFold(k, "content-type") {
			ct = v
			break
		}
	}
	if ct == "" {
		ct = "application/json"
	}
	w.Header().Set("Content-Type", ct)
	for k, v := range headers {
		switch strings.ToLower(k) {
		case "connection", "transfer-encoding", "content-length", "content-type":
			continue
		default:
			w.Header().Set(k, v)
		}
	}
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeBifrostError(w http.ResponseWriter, bErr *schemas.BifrostError) {
	status := bErr.EffectiveHTTPStatus()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(bErr)
}
