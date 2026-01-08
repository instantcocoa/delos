# Observe Service

The **Observe** service is the foundational observability component of the Delos platform. It provides distributed tracing and metrics collection capabilities for all other services, enabling comprehensive visibility into system behavior and performance.

## Overview

- **Port**: 9000 (gRPC)
- **Protocol**: gRPC
- **Dependencies**: None (foundation service)
- **Storage**: In-memory (PostgreSQL backend planned but not yet implemented)

The observe service:
- Ingests trace spans from instrumented services
- Stores and indexes traces for efficient querying
- Provides a query API for trace retrieval and analysis
- Collects and aggregates metrics data
- Serves as the OTLP endpoint for the Delos platform

## API Reference

The service implements `delos.observe.v1.ObserveService` defined in `proto/observe/v1/observe.proto`.

### RPCs

| Method | Description |
|--------|-------------|
| `IngestTraces` | Ingest trace spans from services. Accepts a batch of spans and returns the count of accepted spans. |
| `QueryTraces` | Query traces by various filters (service name, operation, time range, duration, tags). Supports pagination. |
| `GetTrace` | Retrieve a specific trace by its trace ID, including all associated spans. |
| `QueryMetrics` | Query time-series metrics with optional aggregation (sum, avg, min, max, count). |
| `Health` | Health check endpoint returning service status and version. |

### Key Messages

**Span**: Represents a single operation in a distributed trace.
- `trace_id`: Unique identifier for the trace
- `span_id`: Unique identifier for this span
- `parent_span_id`: Parent span (empty for root spans)
- `name`: Operation name
- `service_name`: Service that generated the span
- `start_time`: When the operation started
- `duration`: How long the operation took
- `status`: OK, ERROR, or UNSPECIFIED
- `attributes`: Key-value pairs for additional context
- `events`: Timestamped events within the span

**Trace**: A complete distributed trace containing multiple spans.
- `trace_id`: Unique identifier
- `spans`: All spans belonging to this trace
- `start_time`: Earliest span start time
- `duration`: Total trace duration
- `root_service`: Service name of the root span
- `root_operation`: Operation name of the root span

## Configuration

The service uses environment variables with the `DELOS_` prefix:

### Core Settings

| Variable | Default | Description |
|----------|---------|-------------|
| `DELOS_ENV` | `development` | Environment (development, staging, production) |
| `DELOS_VERSION` | `dev` | Service version |
| `DELOS_GRPC_PORT` | `9000` | gRPC server port |
| `DELOS_LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |
| `DELOS_LOG_FORMAT` | `json` | Log format (json, text) |

### Storage Settings

| Variable | Default | Description |
|----------|---------|-------------|
| `DELOS_STORAGE_BACKEND` | `memory` | Storage backend (currently only `memory` is implemented) |
| `DELOS_DB_HOST` | `localhost` | PostgreSQL host |
| `DELOS_DB_PORT` | `5432` | PostgreSQL port |
| `DELOS_DB_USER` | `delos` | PostgreSQL user |
| `DELOS_DB_PASSWORD` | (empty) | PostgreSQL password |
| `DELOS_DB_NAME` | `delos` | PostgreSQL database name |
| `DELOS_DB_SSLMODE` | `disable` | PostgreSQL SSL mode |

**Note**: The observe service disables its own tracing to avoid circular dependencies (it does not send traces to itself).

## Running Locally

### Prerequisites

- Go 1.22+
- Protocol Buffer compiler (for development)
- PostgreSQL 15+ (optional, for production storage)

### From Source

```bash
# From repository root
make run-observe

# Or directly
go run ./services/observe/cmd/server
```

### With Docker

```bash
# Build
docker build -f services/observe/Dockerfile -t delos-observe .

# Run
docker run -p 9000:9000 delos-observe
```

### With Docker Compose

```bash
# Start all dependencies and services
make up

# Or just dependencies
docker-compose -f deploy/local/docker-compose.yml up -d postgres
```

## Architecture

```
services/observe/
├── cmd/
│   └── server/
│       └── main.go           # Entry point, server setup
├── migrations/
│   ├── 001_initial.up.sql    # Database schema
│   └── 001_initial.down.sql  # Rollback script
├── observe.go                # Domain types (Span, Trace, queries)
├── handler.go                # gRPC handler implementation
├── store.go                  # Storage interfaces and in-memory impl
├── store_test.go             # Unit tests
└── Dockerfile
```

### Storage Layer

The service defines two storage interfaces:

- **SpanStore**: Handles trace/span storage
  - `IngestSpans(ctx, spans)` - Store incoming spans
  - `GetTrace(ctx, traceID)` - Retrieve a complete trace
  - `QueryTraces(ctx, query)` - Search traces with filters

- **MetricStore**: Handles metrics storage
  - `RecordMetric(ctx, name, service, value)` - Record a metric
  - `QueryMetrics(ctx, query)` - Query metrics with filters

Currently implemented:
- `MemorySpanStore` - In-memory storage for development/testing
- `MemoryMetricStore` - In-memory metrics storage

### Database Schema (Planned)

When PostgreSQL backend is implemented, the service will use the following tables:

- `traces` - Trace metadata (trace_id, root service/operation, timing)
- `spans` - Individual spans linked to traces
- `span_attributes` - Key-value attributes for spans
- `span_events` - Events that occurred during a span
- `span_event_attributes` - Attributes for span events
- `metrics` - Time-series metric data

A `cleanup_old_traces(retention_days)` function is provided for data retention.

## Example Usage

### Using grpcurl

```bash
# Health check
grpcurl -plaintext localhost:9000 delos.observe.v1.ObserveService/Health

# Ingest a span
grpcurl -plaintext -d '{
  "spans": [{
    "trace_id": "abc123",
    "span_id": "span1",
    "name": "my-operation",
    "service_name": "my-service",
    "start_time": "2025-01-08T12:00:00Z",
    "duration": "0.100s",
    "status": "SPAN_STATUS_OK"
  }]
}' localhost:9000 delos.observe.v1.ObserveService/IngestTraces

# Get a trace
grpcurl -plaintext -d '{"trace_id": "abc123"}' \
  localhost:9000 delos.observe.v1.ObserveService/GetTrace

# Query traces by service
grpcurl -plaintext -d '{
  "service_name": "my-service",
  "limit": 10
}' localhost:9000 delos.observe.v1.ObserveService/QueryTraces
```

### Using the Python SDK

```python
from delos import ObserveClient

async with ObserveClient("localhost:9000") as client:
    # Check health
    health = await client.health()
    print(f"Status: {health.status}, Version: {health.version}")

    # Query recent traces
    traces = await client.query_traces(
        service_name="runtime",
        limit=10
    )
    for trace in traces:
        print(f"Trace {trace.trace_id}: {trace.root_operation} ({trace.duration})")
```

### Using Go

```go
import (
    "context"
    observev1 "github.com/instantcocoa/delos/gen/go/observe/v1"
    "google.golang.org/grpc"
)

conn, _ := grpc.Dial("localhost:9000", grpc.WithInsecure())
client := observev1.NewObserveServiceClient(conn)

// Query traces
resp, _ := client.QueryTraces(context.Background(), &observev1.QueryTracesRequest{
    ServiceName: "runtime",
    Limit:       10,
})

for _, trace := range resp.Traces {
    fmt.Printf("Trace %s: %s\n", trace.TraceId, trace.RootOperation)
}
```

## Testing

```bash
# Run unit tests
go test ./services/observe/...

# Run with coverage
go test -cover ./services/observe/...

# Run specific test
go test -run TestMemorySpanStore_IngestAndGet ./services/observe/
```

## Metrics Emitted

The observe service itself emits the following operational metrics:

- `observe.spans.ingested` - Count of spans ingested
- `observe.traces.queried` - Count of trace queries
- `observe.latency.ingest` - Latency of span ingestion
- `observe.latency.query` - Latency of trace queries

## Troubleshooting

### Service won't start

1. Check if port 9000 is available: `lsof -i :9000`
2. Verify environment variables are set correctly
3. Check logs for configuration errors

### Traces not appearing

1. Verify services are configured with `DELOS_OBSERVE_ENDPOINT=localhost:9000`
2. Check that `DELOS_TRACING_ENABLED=true` on client services
3. Use the Health endpoint to verify the service is running

### High memory usage (with memory backend)

The in-memory storage grows unbounded. For production use cases:
1. Restart the service periodically to clear memory
2. PostgreSQL backend is planned but not yet implemented
