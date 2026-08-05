# services/datasets

Test data management: datasets of input/expected-output examples that eval runs
score prompts against. This is a Go library, not a process: `controlplane/`
mounts it into the **`delos`** binary, so it is served by `delos serve` on port
**8081** alongside observe, prompt, eval and deploy.

## Public surface

gRPC service `delos.datasets.v1.DatasetsService`, defined in
[`proto/datasets/v1/datasets.proto`](../../proto/datasets/v1/datasets.proto):

| RPC | Description |
|-----|-------------|
| `CreateDataset` / `GetDataset` / `UpdateDataset` / `DeleteDataset` | Dataset CRUD |
| `ListDatasets` | List with filters (prompt, tags, search) |
| `AddExamples` / `GetExamples` / `RemoveExamples` | Example management |
| `GenerateExamples` | Synthesize examples |
| `ImportExamples` | Load from CSV, JSONL, JSON, or Parquet |
| `ExportExamples` | Write out in the same formats |
| `Health` | Module health |

The eval runner reads examples through a direct in-process call into this
module — not over the network.

## Import/export

`dataformat.go` implements parsers and writers for **CSV, JSONL, JSON and
Parquet**. `datasource.go` implements where the bytes come from: a local file,
inline bytes, an HTTP URL, or S3.

## CLI

```bash
delos datasets list [--prompt <id>] [--tags a,b] [--search text]
delos datasets get <id>
delos datasets create <name> [--description ...] [--prompt <id>] [--tags a,b]
delos datasets examples <dataset-id> [--limit 10] [--shuffle]
delos datasets delete <id>
```

(`dataset` and `ds` are aliases.) Import and export are RPC-only today.

## Storage

In-memory (`NewMemoryStore`). The control plane wires the memory store for
datasets regardless of `DELOS_STORAGE_BACKEND`, so datasets are ephemeral and
lost on restart. A `migrations/` directory exists for the eventual
Postgres-backed store but is not applied yet. Keep datasets you care about in
files and re-import them.

## Tests

```bash
go test ./services/datasets/...
```

No external dependencies required.

## See also

- [GETTING_STARTED.md](../../GETTING_STARTED.md) — datasets in an eval workflow
- [services/eval](../eval/README.md) — what consumes these examples
- [README.md](../../README.md) — the two-binary architecture
