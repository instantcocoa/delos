# Architecture Overview

Delos is a microservices-based platform for managing LLM applications in production.

## System Diagram

```
┌─────────────────────────────────────────────────────────────────────┐
│                         Client Applications                          │
│                    (Python SDK, CLI, Direct gRPC)                    │
└─────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│                            Delos Services                            │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐ │
│  │   Runtime   │  │   Prompt    │  │  Datasets   │  │    Eval     │ │
│  │    :9001    │  │    :9002    │  │    :9003    │  │    :9004    │ │
│  │ LLM Gateway │  │  Versioning │  │  Test Data  │  │   Quality   │ │
│  └─────────────┘  └─────────────┘  └─────────────┘  └─────────────┘ │
│         │                │                │                │         │
│         └────────────────┼────────────────┼────────────────┘         │
│                          ▼                ▼                          │
│  ┌─────────────┐  ┌─────────────┐                                   │
│  │   Deploy    │  │   Observe   │                                   │
│  │    :9005    │  │    :9000    │                                   │
│  │  Rollouts   │  │   Tracing   │                                   │
│  └─────────────┘  └─────────────┘                                   │
└─────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│                           LLM Providers                              │
│     OpenAI    │    Anthropic    │    Gemini    │    Ollama          │
└─────────────────────────────────────────────────────────────────────┘
```

## Services

### Observe (Port 9000)
Foundation service for tracing and metrics.
- Receives OpenTelemetry (OTLP) traces from all services
- Stores and queries traces
- Aggregates metrics for dashboards
- No dependencies on other Delos services

### Runtime (Port 9001)
Unified LLM gateway.
- Abstracts multiple providers (OpenAI, Anthropic, Gemini, Ollama)
- Intelligent routing (cost, latency, quality optimized)
- Automatic failover between providers
- Response caching
- Cost tracking

### Prompt (Port 9002)
Prompt versioning and management.
- Git-like versioning for prompts
- Semantic diff between versions
- Template variable support
- Tags for organization

### Datasets (Port 9003)
Test data management.
- CRUD for test datasets
- Link datasets to prompts
- Example management (input/expected output pairs)
- Auto-generation of test cases

### Eval (Port 9004)
Quality evaluation engine.
- Multiple evaluator types (exact match, semantic similarity, LLM judge)
- Evaluation runs against datasets
- Compare runs to detect regressions
- Quality scoring

### Deploy (Port 9005)
Safe deployment orchestration.
- Deployment workflows with approval gates
- Quality gates (require eval scores)
- Rollback capabilities
- A/B testing support

## Service Dependencies

```
observe (foundation)
    ↑ (all services emit traces)
    │
runtime ←→ prompt ←→ datasets
    │         │         │
    └─────────┴─────────┘
              │
              ▼
            eval
              │
              ▼
           deploy
```

## Data Flow

### Completion Request Flow

```
1. Client sends Complete request to Runtime
2. Runtime resolves prompt_ref via Prompt service (if used)
3. Runtime selects provider based on routing strategy
4. Runtime calls LLM provider
5. Runtime returns response with usage/cost
6. Trace emitted to Observe service
```

### Evaluation Flow

```
1. Client creates EvalRun via Eval service
2. Eval fetches prompt from Prompt service
3. Eval fetches examples from Datasets service
4. For each example:
   a. Eval calls Runtime for completion
   b. Eval scores output against expected
5. Eval aggregates results
6. Results stored and returned
```

### Deployment Flow

```
1. Client creates Deployment via Deploy service
2. Deploy checks quality gates (calls Eval)
3. If gates pass, deployment is approved/auto-approved
4. On failure, automatic rollback triggered
```

## Technology Stack

| Component | Technology |
|-----------|------------|
| Language | Go 1.22+ |
| Communication | gRPC + Protocol Buffers |
| Database | PostgreSQL 15+ |
| Cache | Redis 7+ |
| Message Queue | NATS |
| Tracing | OpenTelemetry |
| SDK | Python 3.11+ |

## Service Architecture Pattern

Each service follows clean architecture:

```
services/{name}/
├── cmd/server/main.go     # Entry point
├── internal/
│   ├── api/               # gRPC handlers
│   ├── domain/            # Business entities
│   ├── repository/        # Data access
│   └── service/           # Business logic
├── migrations/            # SQL migrations
└── Dockerfile
```

## Scalability

- All services are stateless (state in PostgreSQL/Redis)
- Horizontal scaling via multiple replicas
- Load balancing at gRPC level
- Connection pooling for databases

## Security

- All inter-service communication via gRPC (TLS in production)
- API keys stored in environment variables
- No secrets in configuration files
- Role-based access control (planned)
