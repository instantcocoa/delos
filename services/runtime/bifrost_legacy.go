package runtime

import (
	"bytes"
	"io"
	"net/http"
)

// The legacy Delos translators are compiled into the default build so that
// cross-provider routes (and a DELOS_GATEWAY_BIFROST=0 deployment) keep working
// without a separate build flag. They stay reachable through the runtime switch
// for one release, then are deleted.

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
	case "/v1/responses":
		// No legacy Responses translator exists; handled below by the caller.
	}
	return false
}
