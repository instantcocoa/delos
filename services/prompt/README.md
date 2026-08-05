# services/prompt

Versioned prompt templates. This is a Go library, not a process: `controlplane/`
mounts it into the **`delos`** binary, so it is served by `delos serve` on port
**8081** alongside observe, datasets, eval and deploy.

## Public surface

gRPC service `delos.prompt.v1.PromptService`, defined in
[`proto/prompt/v1/prompt.proto`](../../proto/prompt/v1/prompt.proto):

| RPC | Description |
|-----|-------------|
| `CreatePrompt` | Create a prompt (version 1) |
| `GetPrompt` | Fetch by ID, slug, or `slug:version` |
| `UpdatePrompt` | Update — always creates a new version |
| `ListPrompts` | List with filters and pagination |
| `DeletePrompt` | Delete a prompt and its history |
| `GetPromptHistory` | Version history |
| `CompareVersions` | Semantic diff between two versions |
| `Health` | Module health |

Every update creates a new immutable version; nothing is edited in place.

## CLI

```bash
delos prompt list
delos prompt get <id-or-slug>
delos prompt create <name> --system "..." --user "..."
delos prompt update <id> --user "..." --change-description "..."
delos prompt history <id>
delos prompt compare <id> <version-a> <version-b>

# Repo-as-source-of-truth sync (.prompt.yaml files)
delos prompt pull ./prompts       # write every prompt to a directory
delos prompt push ./prompts       # sync local files to the control plane
delos prompt diff ./prompts       # semantic diff, local vs. control plane
delos prompt render <file>        # render with variables, fully local
```

## Storage

`store.go` defines the `Store` interface with two implementations selected by
`DELOS_STORAGE_BACKEND`:

- **postgres** — the production path. SQL lives in `migrations/` (embedded via
  `migrations.go`) and is applied by the control plane at startup under the
  `prompt` schema.
- **memory** — the default; ephemeral, for development and tests.

## Tests

```bash
go test ./services/prompt/...
```

Postgres-backed store tests skip automatically when no database is reachable.
To run them, `make up` first.

## See also

- [docs/api-reference/prompt.md](../../docs/api-reference/prompt.md) — full API reference
- [GETTING_STARTED.md](../../GETTING_STARTED.md) — prompts in a workflow
- [README.md](../../README.md) — the two-binary architecture
