# services/observe

Trace ingestion and query. This is a Go library, not a process: `controlplane/`
mounts it into the **`delos`** binary, so it is served by `delos serve` on port
**8081** alongside prompt, datasets, eval and deploy.

Observe is a *destination* for spans, not a producer. `delos-gateway` emits
`gen_ai.*` spans over OTLP and can point at this module — or at Jaeger, Grafana
Tempo, or Datadog instead. Nothing about the gateway requires observe.

## Public surface

gRPC service `delos.observe.v1.ObserveService`, defined in
[`proto/observe/v1/observe.proto`](../../proto/observe/v1/observe.proto):

| RPC | Description |
|-----|-------------|
| `IngestTraces` | Ingest spans |
| `QueryTraces` | Query traces by service, operation, duration, time range |
| `GetTrace` | Fetch a single trace and its spans |
| `QueryMetrics` | Aggregated metrics (sum, avg, min, max, count) |
| `QueryStats` | Rolled-up request/latency/cost stats |
| `Health` | Module health |

Plus one HTTP endpoint mounted by the control plane on the same port:

| Endpoint | Description |
|----------|-------------|
| `POST /v1/traces` | OTLP/HTTP trace ingest (protobuf and JSON) — accepts spans from any `gen_ai.*` source, not just `delos-gateway` |

`otlp.go` translates OTLP spans into the internal `Span` type; `attrs.go` pulls
the `gen_ai.*` and `delos.*` attributes out of them.

## CLI

```bash
delos observe traces --service delos-gateway --limit 50
delos observe trace <trace-id>
delos observe metrics --name <metric> --aggregation avg
delos observe health
```

For a live view of gateway traffic, use `delos tail` — it reads the gateway's
`GET /v1/events` stream directly and does not involve this module.

## Storage

`store.go` defines `SpanStore` and `MetricStore`, selected by
`DELOS_STORAGE_BACKEND`:

- **postgres** — `store_postgres.go`; SQL in `migrations/`, applied by the
  control plane at startup under the `observe` schema.
- **memory** — the default; ephemeral, for development and tests.

## Tests

```bash
go test ./services/observe/...
```

Postgres-backed store tests skip when no database is reachable; `make up`
first to run them.

## See also

- [docs/otel.md](../../docs/otel.md) — span schema and pointing traces at a backend
- [README.md](../../README.md) — the two-binary architecture
- [docs/architecture/overview.md](../../docs/architecture/overview.md)
