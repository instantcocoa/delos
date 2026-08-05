# services/deploy

**Quality gates only.** A gate is a named set of thresholds that the latest
completed eval run for a prompt must satisfy. Delos does not deploy anything,
does not do rollouts, and does not roll back — it emits a verdict and your
CI/CD system acts on it.

This is a Go library, not a process: `controlplane/` mounts it into the
**`delos`** binary, so it is served by `delos serve` on port **8081** alongside
observe, prompt, datasets and eval.

## Public surface

gRPC service `delos.deploy.v1.DeployService`, defined in
[`proto/deploy/v1/deploy.proto`](../../proto/deploy/v1/deploy.proto):

| RPC | Description |
|-----|-------------|
| `CreateQualityGate` | Create a named gate over a prompt |
| `ListQualityGates` | List gates, optionally filtered by prompt |
| `GetGateVerdict` | Evaluate a gate against the latest completed eval run |
| `Health` | Module health |

Plus one HTTP endpoint mounted by the control plane on the same port, for CI
systems that would rather curl than speak gRPC:

| Endpoint | Description |
|----------|-------------|
| `GET /v1/gates/{gate}/verdict` | The same verdict as JSON |

## Conditions

A condition is `metric<op>threshold`, e.g. `overall_score>=0.8`. Operators are
`>=` and `<=`. Supported metrics map one-to-one onto eval run summary fields:

| Metric | Meaning |
|--------|---------|
| `overall_score` | Mean evaluator score for the run |
| `pass_rate` | Fraction of examples that passed |
| `avg_latency_ms` | Mean completion latency |
| `total_cost_usd` | Total run cost |

A verdict carries `Pass`, the eval run ID it judged, and a human-readable
reason per condition (`overall_score 0.82 >= 0.8: pass`).

The module reads run summaries through the narrow `EvalResults` interface, so
it never imports `services/eval`; the control plane wires the adapter.

## CLI

```bash
delos gate create <name> --prompt <id-or-slug> \
      --condition "overall_score>=0.8" --condition "avg_latency_ms<=2000"
delos gate list [--prompt <id>]
delos gate check <name>       # exit 0 on pass, 1 on fail — drop this in CI
```

`delos deploy` still works as an alias for `delos gate`.

## Storage

In-memory (`NewMemoryStore`). The control plane wires the memory store for
gates regardless of `DELOS_STORAGE_BACKEND`, so gate definitions are lost on
restart; recreate them from your CI config. A `migrations/` directory exists
for the eventual Postgres-backed store but is not applied yet.

## Tests

```bash
go test ./services/deploy/...
```

No external dependencies required.

## See also

- [GETTING_STARTED.md](../../GETTING_STARTED.md) — gates in a CI workflow
- [services/eval](../eval/README.md) — where the run summaries come from
- [docs/deploy.md](../../docs/deploy.md) — running Delos itself in production
