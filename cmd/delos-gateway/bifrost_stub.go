//go:build !bifrost

package main

import "net/http"

// bifrostHandler is a no-op in the default build. The Bifrost spike wiring is
// compiled only with -tags=bifrost.
func bifrostHandler() http.Handler { return nil }
