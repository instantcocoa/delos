package prompt

import "embed"

// Migrations contains the prompt schema migrations, applied by the control
// plane at startup when Postgres storage is enabled.
//
//go:embed migrations/*.sql
var Migrations embed.FS
