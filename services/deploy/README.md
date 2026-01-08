# Deploy Service

The Deploy service manages safe deployments of prompt versions with quality gates, gradual rollouts, and automatic rollback capabilities. It acts as the CI/CD gate layer for LLM prompt changes, ensuring that only versions meeting quality thresholds reach production.

## Overview

The Deploy service provides:

- **Deployment orchestration** - Manage the lifecycle of prompt version deployments
- **Quality gates** - Define and enforce quality requirements before deployment
- **Multiple rollout strategies** - Immediate, gradual, canary, and blue-green deployments
- **Auto-rollback** - Automatically revert deployments that fall below quality thresholds
- **Audit trail** - Track who created, approved, and executed deployments

## Port

The Deploy service runs on port **9005** by default.

## Dependencies

The Deploy service depends on:

- **Eval service** (port 9004) - For quality gate evaluation (eval scores, metrics)
- **PostgreSQL** - For persistent storage (optional; uses in-memory store for development)

## API Reference

### DeployService RPCs

| RPC | Description |
|-----|-------------|
| `CreateDeployment` | Creates a new deployment for a prompt version |
| `GetDeployment` | Retrieves a deployment by ID |
| `ListDeployments` | Lists deployments with optional filters (prompt, environment, status) |
| `ApproveDeployment` | Approves a deployment pending manual approval |
| `RollbackDeployment` | Rolls back a deployment to the previous version |
| `CancelDeployment` | Cancels a pending or in-progress deployment |
| `GetDeploymentStatus` | Gets real-time status including rollout progress and metrics |
| `CreateQualityGate` | Creates a quality gate configuration for a prompt |
| `ListQualityGates` | Lists all quality gates for a prompt |
| `Health` | Health check endpoint |

### Proto Definition

The full API is defined in `/proto/deploy/v1/deploy.proto`.

## Deployment States

Deployments follow a state machine with these statuses:

```
                                    +----------------+
                                    |   CANCELLED    |
                                    +----------------+
                                           ^
                                           | CancelDeployment
+----------------------+                   |
|  PENDING_APPROVAL    |-------------------+
+----------------------+                   |
         |                                 |
         | ApproveDeployment               |
         | (or skip_approval=true)         |
         v                                 |
+----------------------+                   |
|   PENDING_GATES      |-------------------+
+----------------------+
         |
         | Gate evaluation
         |
    +----+----+
    |         |
    v         v
+--------+ +-------------+
| GATES  | | IN_PROGRESS |
| FAILED | +-------------+
+--------+        |
                  | Rollout completes
                  |
    +-------------+-------------+
    |             |             |
    v             v             v
+-----------+ +-----------+ +--------+
| COMPLETED | | ROLLED    | | FAILED |
|           | | BACK      | |        |
+-----------+ +-----------+ +--------+
```

### State Descriptions

| Status | Description |
|--------|-------------|
| `PENDING_APPROVAL` | Awaiting manual approval before gate evaluation |
| `PENDING_GATES` | Quality gates are being evaluated |
| `GATES_FAILED` | One or more required quality gates failed |
| `IN_PROGRESS` | Deployment is actively rolling out |
| `COMPLETED` | Deployment finished successfully |
| `ROLLED_BACK` | Deployment was rolled back (manual or automatic) |
| `CANCELLED` | Deployment was cancelled before completion |
| `FAILED` | Deployment failed during rollout |

## Deployment Strategies

### Immediate

Switches all traffic to the new version instantly.

```json
{
  "type": "DEPLOYMENT_TYPE_IMMEDIATE"
}
```

### Gradual

Incrementally shifts traffic to the new version over time.

```json
{
  "type": "DEPLOYMENT_TYPE_GRADUAL",
  "initial_percentage": 10,
  "increment": 10,
  "interval_seconds": 300
}
```

### Canary

Deploys to a small percentage first, then promotes to full traffic after validation.

```json
{
  "type": "DEPLOYMENT_TYPE_CANARY",
  "initial_percentage": 5,
  "auto_rollback": true,
  "rollback_threshold": 0.8
}
```

### Blue-Green

Maintains two full environments, switching traffic atomically.

```json
{
  "type": "DEPLOYMENT_TYPE_BLUE_GREEN"
}
```

## Quality Gates

Quality gates define conditions that must pass before a deployment can proceed.

### Gate Conditions

| Type | Description |
|------|-------------|
| `eval_score` | Evaluation score from the Eval service must meet threshold |
| `latency` | Average latency must meet threshold |
| `cost` | Cost per request must meet threshold |
| `custom` | Custom condition evaluated externally |

### Operators

- `gte` - Greater than or equal to threshold
- `lte` - Less than or equal to threshold
- `eq` - Equal to threshold

### Example Quality Gate

```json
{
  "name": "Production Quality Gate",
  "prompt_id": "prompt-123",
  "required": true,
  "conditions": [
    {
      "type": "eval_score",
      "operator": "gte",
      "threshold": 0.85,
      "dataset_id": "golden-dataset"
    },
    {
      "type": "latency",
      "operator": "lte",
      "threshold": 500
    }
  ]
}
```

## Auto-Rollback

When enabled, deployments automatically roll back if quality metrics fall below the configured threshold during rollout.

```json
{
  "strategy": {
    "type": "DEPLOYMENT_TYPE_GRADUAL",
    "auto_rollback": true,
    "rollback_threshold": 0.7
  }
}
```

The service monitors:
- **Quality score** - From ongoing evaluations
- **Error rate** - HTTP/gRPC error responses
- **Latency** - Response time degradation

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `DELOS_ENV` | Environment (development, staging, production) | `development` |
| `DELOS_VERSION` | Service version | `dev` |
| `DELOS_GRPC_PORT` | gRPC server port (overridden to 9005) | `9005` |
| `DELOS_STORAGE_BACKEND` | Storage backend (`memory` or `postgres`) | `memory` |
| `DELOS_DB_HOST` | PostgreSQL host | `localhost` |
| `DELOS_DB_PORT` | PostgreSQL port | `5432` |
| `DELOS_DB_USER` | PostgreSQL user | `delos` |
| `DELOS_DB_PASSWORD` | PostgreSQL password | (empty) |
| `DELOS_DB_NAME` | PostgreSQL database name | `delos` |
| `DELOS_DB_SSLMODE` | PostgreSQL SSL mode | `disable` |
| `DELOS_REDIS_URL` | Redis URL for caching | `redis://localhost:6379` |
| `DELOS_OBSERVE_ENDPOINT` | Observe service endpoint for tracing | `localhost:9000` |
| `DELOS_LOG_LEVEL` | Log level (debug, info, warn, error) | `info` |
| `DELOS_LOG_FORMAT` | Log format (json, text) | `json` |
| `DELOS_TRACING_ENABLED` | Enable OpenTelemetry tracing | `true` |
| `DELOS_TRACING_SAMPLING` | Tracing sample rate (0.0-1.0) | `1.0` |

## Running Locally

### Using Make

```bash
# From repository root
make run-deploy
```

### Direct Execution

```bash
# Build
go build -o bin/deploy ./services/deploy/cmd/server

# Run
./bin/deploy
```

### With Docker

```bash
# Build image
docker build -t delos-deploy -f services/deploy/Dockerfile .

# Run container
docker run -p 9005:9005 delos-deploy
```

### With Docker Compose

```bash
# Start all services including deploy
make up

# Or just start deploy and its dependencies
docker compose -f deploy/local/docker-compose.yml up deploy
```

## Database Schema

The service uses the following PostgreSQL tables (see `migrations/001_initial.up.sql`):

- `deployments` - Core deployment records
- `deployment_strategies` - Rollout strategy configuration
- `deployment_rollouts` - Gradual rollout progress tracking
- `deployment_metadata` - Key-value metadata for deployments
- `quality_gates` - Quality gate definitions
- `quality_gate_conditions` - Individual gate conditions
- `deployment_gate_results` - Gate evaluation results per deployment
- `deployment_condition_results` - Individual condition results
- `deployment_metrics` - Metrics snapshots during rollout

## Architecture

### Components

```
services/deploy/
├── cmd/server/main.go    # Entry point, service initialization
├── deploy.go             # Domain models and types
├── handler.go            # gRPC handler implementation
├── service.go            # Business logic layer
├── store.go              # Storage interface and in-memory implementation
├── store_test.go         # Storage tests
├── migrations/           # PostgreSQL migrations
└── Dockerfile            # Container build
```

### Request Flow

1. Client calls `CreateDeployment` with prompt ID, target version, and strategy
2. Service determines `from_version` from current active deployment
3. If `skip_approval` is false, deployment enters `PENDING_APPROVAL` state
4. After approval (or if skipped), service evaluates quality gates
5. If all required gates pass, deployment transitions to `IN_PROGRESS`
6. For gradual/canary deployments, rollout progresses incrementally
7. Service monitors metrics and may auto-rollback if thresholds are breached
8. Deployment completes successfully or fails/rolls back

### State Machine Implementation

The deployment state transitions are managed in `service.go`:

- `CreateDeployment` - Creates deployment in `PENDING_APPROVAL` or `PENDING_GATES` state
- `ApproveDeployment` - Transitions from `PENDING_APPROVAL` to `PENDING_GATES`
- `evaluateGates` - Evaluates gates and transitions to `IN_PROGRESS` or `GATES_FAILED`
- `RollbackDeployment` - Marks deployment as `ROLLED_BACK` and creates reverse deployment
- `CancelDeployment` - Transitions to `CANCELLED` if in a cancellable state

## Example Usage

### Create a Deployment

```bash
grpcurl -plaintext -d '{
  "prompt_id": "prompt-abc123",
  "to_version": 5,
  "environment": "production",
  "strategy": {
    "type": "DEPLOYMENT_TYPE_GRADUAL",
    "initial_percentage": 10,
    "increment": 20,
    "interval_seconds": 300,
    "auto_rollback": true,
    "rollback_threshold": 0.75
  },
  "skip_approval": false
}' localhost:9005 delos.deploy.v1.DeployService/CreateDeployment
```

### Approve a Deployment

```bash
grpcurl -plaintext -d '{
  "id": "deployment-xyz",
  "comment": "Approved after review"
}' localhost:9005 delos.deploy.v1.DeployService/ApproveDeployment
```

### Create a Quality Gate

```bash
grpcurl -plaintext -d '{
  "name": "Prod Gate",
  "prompt_id": "prompt-abc123",
  "required": true,
  "conditions": [
    {
      "type": "eval_score",
      "operator": "gte",
      "threshold": 0.9,
      "dataset_id": "golden-set"
    }
  ]
}' localhost:9005 delos.deploy.v1.DeployService/CreateQualityGate
```

### Check Deployment Status

```bash
grpcurl -plaintext -d '{
  "id": "deployment-xyz"
}' localhost:9005 delos.deploy.v1.DeployService/GetDeploymentStatus
```

### Rollback a Deployment

```bash
grpcurl -plaintext -d '{
  "id": "deployment-xyz",
  "reason": "Quality regression detected"
}' localhost:9005 delos.deploy.v1.DeployService/RollbackDeployment
```

### List Deployments

```bash
grpcurl -plaintext -d '{
  "prompt_id": "prompt-abc123",
  "environment": "production",
  "limit": 10
}' localhost:9005 delos.deploy.v1.DeployService/ListDeployments
```

## Testing

```bash
# Run unit tests
go test ./services/deploy/...

# Run with coverage
go test -cover ./services/deploy/...

# Run integration tests (requires database)
go test -tags=integration ./services/deploy/tests/integration/...
```

## Related Services

- **Eval** (port 9004) - Provides quality scores for gate evaluation
- **Prompt** (port 9002) - Manages prompt versions being deployed
- **Observe** (port 9000) - Receives traces and metrics for monitoring deployments
