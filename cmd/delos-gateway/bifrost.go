package main

import "net/http"

// bifrostHandler is vestigial from the D0 spike. The real Bifrost wiring lives
// in services/runtime: runtime.NewHTTPServer dispatches every /v1 route to
// Bifrost core by default (see DELOS_GATEWAY_BIFROST and the delos_translators
// build tag). Returning nil keeps the spike's fallback mux in main.go inert;
// that block is dead and is removed by the main.go owner (DL-S) in the
// integration pass.
func bifrostHandler() http.Handler { return nil }
