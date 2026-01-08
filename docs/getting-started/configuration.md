# Configuration Reference

All Delos services are configured via environment variables with the `DELOS_` prefix.

## Common Settings

These settings apply to all services:

| Variable | Default | Description |
|----------|---------|-------------|
| `DELOS_ENV` | `development` | Environment name (development, staging, production) |
| `DELOS_LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |
| `DELOS_LOG_FORMAT` | `json` | Log format (json, text) |
| `DELOS_DB_HOST` | `localhost` | PostgreSQL host |
| `DELOS_DB_PORT` | `5432` | PostgreSQL port |
| `DELOS_DB_USER` | `delos` | PostgreSQL username |
| `DELOS_DB_PASSWORD` | `delos` | PostgreSQL password |
| `DELOS_DB_NAME` | `delos` | PostgreSQL database name |
| `DELOS_REDIS_URL` | `redis://localhost:6379` | Redis connection URL |
| `DELOS_NATS_URL` | `nats://localhost:4222` | NATS connection URL |
| `DELOS_OBSERVE_ENDPOINT` | `localhost:9000` | Observe service endpoint for tracing |

## Runtime Service (Port 9001)

LLM gateway configuration:

| Variable | Default | Description |
|----------|---------|-------------|
| `DELOS_RUNTIME_OPENAI_KEY` | - | OpenAI API key |
| `DELOS_RUNTIME_ANTHROPIC_KEY` | - | Anthropic API key |
| `DELOS_RUNTIME_GEMINI_KEY` | - | Google Gemini API key |
| `DELOS_RUNTIME_OLLAMA_ENABLED` | `false` | Enable Ollama provider |
| `DELOS_RUNTIME_OLLAMA_URL` | `http://localhost:11434` | Ollama server URL |
| `DELOS_RUNTIME_OPENROUTER_KEY` | - | OpenRouter API key |
| `DELOS_RUNTIME_OPENROUTER_SITE_URL` | - | Your site URL (for rankings) |
| `DELOS_RUNTIME_OPENROUTER_SITE_NAME` | - | Your site name (for rankings) |
| `GOOGLE_CLOUD_PROJECT` | - | GCP project ID (Vertex AI) |
| `GOOGLE_CLOUD_LOCATION` | `us-central1` | GCP region (Vertex AI) |
| `GOOGLE_CLOUD_ACCESS_TOKEN` | - | OAuth2 access token (Vertex AI) |
| `DELOS_RUNTIME_TOGETHER_KEY` | - | Together AI API key |
| `DELOS_RUNTIME_CACHE_ENABLED` | `true` | Enable response caching |
| `DELOS_RUNTIME_CACHE_TTL` | `3600` | Cache TTL in seconds |

## Prompt Service (Port 9002)

Prompt versioning configuration:

| Variable | Default | Description |
|----------|---------|-------------|
| `DELOS_PROMPT_MAX_VERSIONS` | `100` | Maximum versions per prompt |

## Datasets Service (Port 9003)

Test data management configuration:

| Variable | Default | Description |
|----------|---------|-------------|
| `DELOS_DATASETS_MAX_EXAMPLES` | `10000` | Maximum examples per dataset |

## Eval Service (Port 9004)

Evaluation configuration:

| Variable | Default | Description |
|----------|---------|-------------|
| `DELOS_EVAL_PARALLEL` | `5` | Parallel evaluation workers |
| `DELOS_EVAL_TIMEOUT` | `300` | Evaluation timeout in seconds |

## Deploy Service (Port 9005)

Deployment configuration:

| Variable | Default | Description |
|----------|---------|-------------|
| `DELOS_DEPLOY_REQUIRE_APPROVAL` | `true` | Require manual approval for deployments |
| `DELOS_DEPLOY_AUTO_ROLLBACK` | `true` | Enable automatic rollback on failures |

## Observe Service (Port 9000)

Tracing and metrics configuration:

| Variable | Default | Description |
|----------|---------|-------------|
| `DELOS_OBSERVE_RETENTION_DAYS` | `30` | Trace retention in days |

## Example .env File

```bash
# Environment
DELOS_ENV=development
DELOS_LOG_LEVEL=info

# Database
DELOS_DB_HOST=localhost
DELOS_DB_PORT=5432
DELOS_DB_USER=delos
DELOS_DB_PASSWORD=delos
DELOS_DB_NAME=delos

# Cache
DELOS_REDIS_URL=redis://localhost:6379

# Message Queue
DELOS_NATS_URL=nats://localhost:4222

# LLM Providers
DELOS_RUNTIME_OPENAI_KEY=sk-...
DELOS_RUNTIME_ANTHROPIC_KEY=sk-ant-...
DELOS_RUNTIME_GEMINI_KEY=AIza...
DELOS_RUNTIME_OLLAMA_ENABLED=true
DELOS_RUNTIME_OPENROUTER_KEY=sk-or-...
DELOS_RUNTIME_TOGETHER_KEY=...

# Vertex AI (Google Cloud)
GOOGLE_CLOUD_PROJECT=your-project-id
GOOGLE_CLOUD_LOCATION=us-central1

# AWS Bedrock
AWS_ACCESS_KEY_ID=AKIA...
AWS_SECRET_ACCESS_KEY=...
AWS_REGION=us-east-1
```

## Docker Compose Override

For local development, create `deploy/local/.env.local`:

```bash
# Local overrides (not committed to git)
DELOS_RUNTIME_OPENAI_KEY=sk-your-actual-key
```
