//go:build bifrost

// The governance, otel and logging plugins are pinned alongside Bifrost core
// in this spike (D0) so the go.mod/go.sum dependency shape is recorded. They
// are wired up for real by the D1/D2 teams; these blank imports only keep them
// in the module graph and prove they compile against Bifrost core at d913ed8.
package main

import (
	_ "github.com/maximhq/bifrost/plugins/governance"
	_ "github.com/maximhq/bifrost/plugins/logging"
	_ "github.com/maximhq/bifrost/plugins/otel"
)
