# services/eval

Quality scoring and regression testing: run a prompt against a dataset, score
the outputs with evaluators, compare runs. This is a Go library, not a process:
`controlplane/` mounts it into the **`delos`** binary, so it is served by
`delos serve` on port **8081** alongside observe, prompt, datasets and deploy.

## Public surface

gRPC service `delos.eval.v1.EvalService`, defined in
[`proto/eval/v1/eval.proto`](../../proto/eval/v1/eval.proto):

| RPC | Description |
|-----|-------------|
| `CreateEvalRun` | Queue a run over (prompt version, dataset, model, evaluators) |
| `GetEvalRun` / `ListEvalRuns` | Run status and listing |
| `CancelEvalRun` | Cancel a queued or running evaluation |
| `GetEvalResults` | Per-example results and scores |
| `CompareRuns` | Diff two runs to spot regressions |
| `ListEvaluators` | Available evaluator types |
| `Health` | Module health |

## The runner

`runner.go` is a background worker started by the control plane. It fetches the
prompt and examples through **direct in-process calls** into the prompt and
datasets modules, and sends completions to **`delos-gateway` over its public
OpenAI-compatible HTTP API** (`DELOS_GATEWAY_URL`, `clients.go`) — the same way
your application would. It never imports provider code.

`gates.go` exposes `RunSummaryForGates`, the narrow view of a completed run
that `services/deploy` consumes; the dependency runs
`eval <- controlplane -> deploy`, so neither module imports the other.

## Evaluators

Registered in `evaluators.go`:

| Type | Checks |
|------|--------|
| `exact_match` | Output equals expected |
| `contains` | Output contains expected (`case_sensitive` param) |
| `regex` | Output matches a `pattern` param |
| `json_schema` | Output parses as JSON and validates against a `schema` param |

## CLI

```bash
delos eval run --prompt <id> --dataset <id> --model claude-haiku-4-5 \
               --evaluators exact_match,contains [--name ...] [--version N]
delos eval list [--prompt <id>] [--dataset <id>]
delos eval get <id>
delos eval results <run-id> [--failed] [--limit 20]
delos eval compare <run-a> <run-b>
delos eval cancel <id>
delos eval evaluators
```

`--model` is required: the run needs to know what to send to the gateway.

## Storage

In-memory (`NewMemoryStore`). The control plane wires the memory store for eval
regardless of `DELOS_STORAGE_BACKEND`, so runs and results are lost on restart.
A `migrations/` directory exists for the eventual Postgres-backed store but is
not applied yet.

## Tests

```bash
go test ./services/eval/...
```

No external dependencies required — the runner tests use a fake completion
client.

## See also

- [GETTING_STARTED.md](../../GETTING_STARTED.md) — the prompt → dataset → eval → gate workflow
- [services/deploy](../deploy/README.md) — turning run summaries into CI verdicts
- [README.md](../../README.md) — the two-binary architecture
