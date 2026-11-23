//go:build !delos_translators

package runtime

import (
	"net/http"
)

// legacyTranslatorsCompiled is false in a Bifrost-only build, so the
// DELOS_GATEWAY_BIFROST switch has nothing to switch to and Bifrost always
// serves.
const legacyTranslatorsCompiled = false

// registerLegacyRoutes is a no-op here; NewHTTPServer never reaches the legacy
// branch in this build (useBifrostPath is constant true).
func registerLegacyRoutes(s *HTTPServer) {}

// serveLegacyRoute reports no legacy handler: cross-provider translation is not
// compiled into a Bifrost-only build.
func (s *HTTPServer) serveLegacyRoute(http.ResponseWriter, *http.Request, []byte) bool { return false }
