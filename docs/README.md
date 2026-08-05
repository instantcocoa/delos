# Delos Documentation

Delos is infrastructure for LLM applications: an OpenAI/Anthropic-compatible
gateway plus prompt versioning, evaluation, CI quality gates, and observability.
It ships as two binaries and one PostgreSQL database.

| Binary | Role | Port |
|--------|------|------|
| `delos-gateway` | Data plane - stateless LLM gateway | 8080 |
| `delos` | Control plane (`delos serve`) + CLI | 8081 |

## Quick Links

### Getting Started
- [Installation](getting-started/installation.md) - `go install`, Docker, signed release binaries
- [Quickstart](getting-started/quickstart.md) - 5-minute tutorial, starting with a single `docker run`
- [Configuration](getting-started/configuration.md) - `delos.yaml` and every environment variable

### Running it
- [Providers](providers.md) - Per-provider setup and the feature matrix, including what does *not* work
- [OpenTelemetry](otel.md) - Point exporters at Jaeger, Grafana Tempo or Datadog
- [Deploying](deploy.md) - TLS, Postgres sizing, HA gateways behind a load balancer
- [Troubleshooting](../TROUBLESHOOTING.md) - When something is wrong

### Architecture
- [Overview](architecture/overview.md) - Data plane vs control plane, request flow

### API Reference
- [Gateway API](api-reference/runtime.md) - OpenAI/Anthropic-compatible HTTP endpoints
- [Prompt API](api-reference/prompt.md) - Prompt versioning (control plane gRPC)

### Clients
There is no Delos-specific SDK. Point any OpenAI- or Anthropic-compatible client
at the gateway's base URL. The `delos` CLI covers control-plane operations.

## Ports

| Component | Port | Purpose |
|-----------|------|---------|
| `delos-gateway` | 8080 | LLM gateway (HTTP) |
| `delos serve` | 8081 | Control plane: observe, prompt, datasets, eval, quality gates (gRPC over h2c + `/healthz`) |
| PostgreSQL | 5432 | Control-plane storage |

Optional Docker Compose profiles add Ollama (11434), Jaeger UI (16686), and
LocalStack (4566); none are started by default.

## Quick Example

Send traffic through the gateway with an unmodified OpenAI client:

```python
from openai import OpenAI

client = OpenAI(base_url="http://localhost:8080/v1", api_key="delos-dev")
response = client.chat.completions.create(
    model="gpt-4o",
    messages=[{"role": "user", "content": "Summarize this article..."}],
)
print(response.choices[0].message.content)
```

Manage prompts, datasets, evaluations, and quality gates with the CLI:

```bash
delos tail                    # live view of every request through the gateway
delos config doctor           # is everything reachable and configured?
delos prompt push ./prompts   # prompts are files; the repo is the source of truth
delos eval evaluators
delos gate check nightly      # exit 0/1 for CI
delos gateway models
```

## Executable documentation

Code blocks whose first line is `# docs-test` are extracted and run in CI by
`make docs-test`. If a documented command stops working, the build fails. Blocks
that need Docker, provider credentials or a running stack are intentionally left
unmarked.

## Getting Help

- [GitHub Issues](https://github.com/instantcocoa/delos/issues) - Bug reports and feature requests
- [Discussions](https://github.com/instantcocoa/delos/discussions) - Questions and community
