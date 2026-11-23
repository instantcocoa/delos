//go:build delos_translators

package runtime

import (
	"bytes"
	"io"
	"net/http"
)

// legacyTranslatorsCompiled gates the DELOS_GATEWAY_BIFROST runtime switch: the
// legacy translators only exist in a -tags=delos_translators build, so a build
// without the tag always serves Bifrost.
const legacyTranslatorsCompiled = true

// registerLegacyRoutes wires the legacy Delos translator handlers onto the
// shared mux. /v1/events and /healthz are shared and registered by NewHTTPServer.
func registerLegacyRoutes(s *HTTPServer) {
	s.mux.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)
	s.mux.HandleFunc("POST /v1/embeddings", s.handleEmbeddings)
	s.mux.HandleFunc("GET /v1/models", s.handleListModels)
	s.mux.HandleFunc("GET /v1/models/{model}", s.handleGetModel)
	s.mux.HandleFunc("POST /v1/messages", s.handleAnthropicMessages)
}

// serveLegacyRoute hands a cross-provider request (already authenticated and
// with the body read) to the matching legacy translator. It reports whether a
// legacy handler claimed the request.
func (s *HTTPServer) serveLegacyRoute(w http.ResponseWriter, r *http.Request, body []byte) bool {
	r.Body = io.NopCloser(bytes.NewReader(body))
	switch r.URL.Path {
	case "/v1/messages":
		s.handleAnthropicMessages(w, r)
		return true
	case "/v1/chat/completions":
		s.handleChatCompletions(w, r)
		return true
	case "/v1/embeddings":
		s.handleEmbeddings(w, r)
		return true
	}
	return false
}
