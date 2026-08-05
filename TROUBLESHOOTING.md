# Troubleshooting Guide

Common issues and solutions when working with Delos.

Delos is two binaries plus Postgres:

| Process | Port | Needs a database? |
|---------|------|-------------------|
| `delos-gateway` (data plane, HTTP) | 8080 | No |
| `delos serve` (control plane, gRPC over h2c + `/healthz`) | 8081 | Optional (memory by default) |
| PostgreSQL | 5432 | — |

First thing to run for almost any issue:

```bash
delos config doctor
```

It checks the config file, the control plane, the gateway, PostgreSQL (when
configured) and provider connectivity, and prints a fix hint for anything that
fails:

```
ok    config file    /srv/delos/delos.yaml parses cleanly
ok    control plane  localhost:8081 healthy ({"status":"ok"})
FAIL  gateway        cannot reach http://localhost:8080/healthz: connection refused
      -> start it with `delos-gateway` (or `make run-gateway`), or set DELOS_GATEWAY_URL
```

By hand, the same first two checks are:

```bash
curl localhost:8080/healthz   # {"status":"ok","providers":N}
curl localhost:8081/healthz   # {"status":"ok"}
```

Second thing: watch what the gateway is actually doing.

```bash
delos tail --replay 20
```

Every request appears with its status, provider, latency, cost and error code -
usually that is the whole diagnosis.

## Gateway Issues (port 8080)

### Gateway not responding

**Error:** `connection refused` from `curl localhost:8080/healthz`, or `failed to reach gateway at http://localhost:8080` from the CLI.

**Check:**
1. Is it running?
   ```bash
   pgrep -fa delos-gateway
   docker compose -f deploy/local/docker-compose.yaml ps gateway
   ```

2. Start it:
   ```bash
   make run-gateway   # foreground, local binary
   make up-all        # everything in Docker (postgres + gateway + control plane)
   ```

3. Is it on a different port? The gateway listens on 8080 unless `DELOS_GATEWAY_PORT` says otherwise. Clients look for `DELOS_GATEWAY_URL` (default `http://localhost:8080`).

### "no LLM providers configured"

**Warning at startup:**
```
no LLM providers configured - set OPENAI_API_KEY, ANTHROPIC_API_KEY, GEMINI_API_KEY, AWS credentials, or DELOS_RUNTIME_OLLAMA_ENABLED=true
```

The gateway starts fine but every completion returns `404 model_not_found`. `GET /healthz` reports `"providers": 0`.

**Solution:**
1. Set at least one provider credential. The gateway reads plain provider variable names:
   ```bash
   export OPENAI_API_KEY=sk-...
   export ANTHROPIC_API_KEY=sk-ant-...
   export GEMINI_API_KEY=AIza...          # or GOOGLE_API_KEY
   export AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=...   # Bedrock (or AWS_PROFILE)
   export AWS_REGION=us-east-1                              # Bedrock region
   export DELOS_RUNTIME_OLLAMA_ENABLED=true                 # local models
   export DELOS_COMPAT_BASE_URL=https://...  DELOS_COMPAT_NAME=...  # any OpenAI-compatible endpoint
   ```
   Those are all of them. There is no OpenRouter, Together or Vertex AI
   provider — reach those through `DELOS_COMPAT_*`.
   (The older `DELOS_RUNTIME_<PROVIDER>_KEY` names still work as fallbacks.)

2. For Docker Compose, put keys in `deploy/local/.env.local` (gitignored) — both containers load it.

3. **Restart the gateway.** Providers are registered once at startup; there is no hot reload.

4. Confirm:
   ```bash
   curl -s localhost:8080/healthz
   delos gateway models
   ```

### `model_not_found`

**Error:**
```json
{"error": {"message": "model \"gpt-5\" is not served by any configured provider ...", "code": "model_not_found"}}
```

**Cause:** No configured provider claims that model name.

**Check:**
1. What is actually available?
   ```bash
   delos gateway models     # or: curl -s localhost:8080/v1/models
   ```

2. Prefix the model with the provider name to be explicit:
   ```bash
   delos gateway complete "hi" --model openai/gpt-4o
   delos gateway complete "hi" --model ollama/gemma3:4b
   ```

Model resolution order is: `provider/model` prefix, then an exact match in a provider's model list, then well-known prefixes (`gpt-`/`o1`/`o3`/`o4`/`text-embedding-` → openai, `claude-` → anthropic, `gemini-` → gemini).

### Provider authentication errors

**Error:** `502` with `code: "provider_error"` and a message like `provider openai: ... 401 ... invalid_api_key`.

A 401/403 wrapped in a `provider_error` always comes from the upstream provider. The gateway's *own* rejections of a client virtual key are plain `401 invalid_api_key` / `403 model_not_allowed`, never `provider_error`.

**Check:**
1. The key is valid and not truncated (watch for quotes or trailing whitespace in `.env.local`).
2. The key matches the provider you are routing to (an OpenAI key will not work for `anthropic/...`).
3. Bedrock: credentials need `bedrock:InvokeModel` and model access enabled in the console; check `AWS_REGION`.
4. OpenAI-compatible endpoints: `DELOS_COMPAT_API_KEY` is sent as a Bearer token. If the backend wants a different header, put a proxy in front of it.

### No Ollama models

**Symptom:** no Ollama models appear in `delos gateway models`, or requests to `ollama/...` fail with a connection error.

**Cause:** the gateway registers the Ollama provider whenever `DELOS_RUNTIME_OLLAMA_ENABLED=true` or `DELOS_RUNTIME_OLLAMA_URL` is set — it does not probe first, so a registered-but-unreachable Ollama fails at request time, not at startup. Ollama is an optional Docker Compose profile and is **not** started by `make up` / `make up-all`.

**Solution:**
```bash
make up-ollama      # starts the 'ollama' profile on :11434 and pulls gemma3:4b
make ollama-ready   # verify Ollama and the model are up
```

Also check the URL: inside Compose use `DELOS_RUNTIME_OLLAMA_URL=http://ollama:11434`; from the host, `http://localhost:11434` (the default).

### Streaming stops early

**Symptom:** an SSE response ends with a `data:` frame containing `"code": "provider_stream_error"` and no `data: [DONE]`.

This is deliberate: the upstream stream broke after headers were sent, so the gateway signals the failure in-band rather than letting a truncated answer look complete. The message names the provider — retry or check the provider's status.

### Config file problems

**Error:** the binary refuses to start with `delos.yaml is invalid: ...`, or an
option you set has no effect.

**Check the file first** - `validate` is offline and instant:

```bash
delos config validate delos.yaml
```

```
INVALID delos.yaml (2 problem(s))
  line 7: unknown option "prot" (check the spelling against delos.yaml.example)
  gateway.cache.ttl: "5 minutes" is not a duration; use values like "30s", "5m" or "1h"
```

**An option is ignored:** precedence is **environment variable > `delos.yaml` >
default**, so an exported `DELOS_*` variable silently wins over the file. Print
the environment to find the culprit:

```bash
env | grep '^DELOS_'
```

**The file is not being read at all:** Delos looks at `$DELOS_CONFIG`, then
`./delos.yaml` *relative to the working directory of the process* - which in a
container or a systemd unit is often not where you think. The gateway logs the
file it loaded at startup (`config_file=...`, or `(none)`).

**A provider key set in the file does nothing:** that is deliberate. Secrets are
environment-only (`OPENAI_API_KEY`, `DELOS_DB_PASSWORD`, ...) and the file
rejects unknown keys, so it will actually fail validation.

### `delos tail` shows nothing

1. **Nothing is flowing.** Send a request in another terminal; the event appears
   immediately. `--replay 20` shows recent history to prove the stream works.
2. **`this gateway is not streaming events`** - the gateway was started with
   `DELOS_TAIL=off`. Restart it without that variable.
3. **401 from the tail stream** - virtual keys are enforced; export
   `DELOS_API_KEY=<key from delos key create>`.
4. **You are tailing through a load balancer.** `/v1/events` is per-process: you
   see one replica's traffic. Tail a specific pod or container when debugging.
5. **Events are missing under heavy load.** The stream is deliberately lossy: a
   slow reader drops events rather than slowing the gateway down. Traces
   (`docs/otel.md`) are the complete record; `tail` is the live view.

### Virtual key errors (401 / 403 / 429)

| Response | Meaning | Fix |
|----------|---------|-----|
| `401 invalid_api_key` | Keys are enforced and the presented key is unknown or revoked | `delos key create <name>`, then pass it as the client's `api_key` |
| `403 model_not_allowed` | The key is scoped to other models | Recreate the key with the right `--models`, e.g. `--models 'openai/*'` |
| `429 budget_exceeded` | The key's monthly token or dollar budget is spent | Raise the budget on a new key, or wait for the month to roll over |
| `503 budget_unavailable` | The gateway could not read usage from Postgres | Check the database; the gateway refuses rather than letting spend run unmetered |

If you expected **no** authentication and are getting 401s, the gateway has a
database configured, which turns enforcement on. Dev mode (no database) logs
`virtual keys are NOT enforced` at startup - if you do not see that line, keys
are live.

## Control Plane Issues (port 8081)

### Control plane won't connect to Postgres

**Error:** `failed to connect to database` or `connection refused` at startup.

**Check:**
1. Is PostgreSQL running?
   ```bash
   make up   # starts postgres only
   docker compose -f deploy/local/docker-compose.yaml ps postgres
   # look for "(healthy)"
   ```

2. Is the hostname right?
   - Inside Docker: `DELOS_DB_HOST=postgres` (the service name)
   - Outside Docker: `DELOS_DB_HOST=localhost`

3. Do you even need Postgres? The default is `DELOS_STORAGE_BACKEND=memory`, which needs no database (and loses data on restart). Unset `DELOS_STORAGE_BACKEND` to fall back to memory while debugging.

### Migration failures

**Error:** `failed to apply prompt migrations` at startup.

Migrations run automatically inside `delos serve` when `DELOS_STORAGE_BACKEND=postgres` — there is no separate migrate command.

**Check:**
1. The database user can create tables:
   ```bash
   docker exec -it delos-postgres psql -U delos -d delos -c '\dt'
   ```
2. A partially-migrated database from an older build. For local development, wipe and recreate:
   ```bash
   docker compose -f deploy/local/docker-compose.yaml down -v
   make up
   ```

### Process exits immediately

**Symptoms:** the binary starts then stops with no obvious error.

**Check logs by running it in the foreground:**
```bash
make run-gateway         # or: ./bin/delos-gateway
make run-control-plane   # or: ./bin/delos serve

# Docker
docker compose -f deploy/local/docker-compose.yaml logs gateway
docker compose -f deploy/local/docker-compose.yaml logs delos
```

**Common causes:**
- Port already in use
- Database not ready (control plane, Postgres backend)
- Malformed `DELOS_*` values

### Port already in use

**Error:** `listen tcp :8080: bind: address already in use` (or `:8081`)

**Solution:**
```bash
# See what has it
lsof -i :8080
lsof -i :8081

# Stop background binaries started by make run-all
make stop-all

# Stop containers
make down

# Or move the listener
DELOS_GATEWAY_PORT=8090 make run-gateway
DELOS_PORT=8091 make run-control-plane
```

Note that `make up-all` publishes 8080 and 8081 on the host, so it conflicts with locally-run binaries. Use one or the other.

## Docker Issues

### Containers won't start

**Error:** `Cannot connect to the Docker daemon`

**Solution:**
1. Start Docker Desktop (macOS/Windows)
2. Or start Docker service (Linux):
   ```bash
   sudo systemctl start docker
   ```

### Out of disk space

**Error:** `no space left on device`

**Solution:**
```bash
# Clean up Docker
docker system prune -a

# Remove volumes too (WARNING: deletes data)
docker system prune -a --volumes
```

### Build fails

**Error:** Dockerfile build errors

Images are built from the repo root: `Dockerfile.gateway` for the gateway, `Dockerfile.delos` for the control plane/CLI.

**Try:**
```bash
docker compose -f deploy/local/docker-compose.yaml down -v
docker compose -f deploy/local/docker-compose.yaml build --no-cache
make up-all
```

## Test Issues

### Integration tests fail with "not available"

**Cause:** The stack isn't running. Integration tests need the gateway and the control plane.

**Solution:**
```bash
# Everything in Docker
make up-all && make test-integration

# Or Postgres in Docker, binaries locally
make up && make run-all && make test-integration
```

See `./tests/integration/run.sh --help` for the runner's options.

### Tests hang

**Cause:** Waiting for dependencies that never become ready.

**Check:**
```bash
docker compose -f deploy/local/docker-compose.yaml ps
docker logs delos-postgres
docker logs delos-gateway
docker logs delos-control-plane
```

`make test` starts Postgres and LocalStack itself; if those never report healthy, the run will stall waiting on them.

### Ollama tests are skipped

**Cause:** The `ollama` profile is off by default and the model is large (gemma3:4b, ~3.3GB).

**Solution:**
```bash
make up-ollama
make ollama-ready
make test-ollama
```

### CLI tests fail with empty output

**Cause:** The binary isn't built or is in the wrong place.

**Solution:**
```bash
make build
ls -la bin/delos bin/delos-gateway
```

## Build Issues

### `go mod download` fails

**Error:** Module download errors

**Try:**
```bash
go clean -modcache
go mod download
```

### Proto generation fails

**Error:** `buf generate` errors

**Check:**
1. Is buf installed?
   ```bash
   which buf
   # If not: make tools
   ```

2. Are proto files valid?
   ```bash
   make proto-lint
   ```

### Missing generated code

**Error:** Import errors for `gen/go/...`

**Solution:**
```bash
make proto
```

### `buf breaking` reports breaking changes

Expected during the two-binary refactor — protos moved and the runtime protos were removed. The check is disabled in CI while the change lands; `make proto-breaking` will still fail locally.

## CLI Issues

### CLI can't connect

The `delos` binary is both the control plane server (`delos serve`) and the client. Client subcommands talk to two different places:

| Commands | Target | Env var | Default |
|----------|--------|---------|---------|
| `prompt`, `datasets`, `eval`, `deploy`, `observe` | control plane (gRPC) | `DELOS_CONTROL_PLANE_ADDR` | `localhost:8081` |
| `gateway` (alias `runtime`) | gateway (HTTP) | `DELOS_GATEWAY_URL` | `http://localhost:8080` |

**Error:** `connection refused` or timeout

**Check:**
1. Is the target running?
   ```bash
   curl localhost:8081/healthz
   delos gateway health
   ```

2. Are you using the right address format? `DELOS_CONTROL_PLANE_ADDR` is a gRPC `host:port` with **no scheme**; `DELOS_GATEWAY_URL` is a full URL **with** scheme:
   ```bash
   DELOS_CONTROL_PLANE_ADDR=localhost:8081 delos prompt list
   DELOS_GATEWAY_URL=http://localhost:8080 delos gateway models
   ```

### Command not found

**Error:** `delos: command not found`

**Solution:** Use the full path or add to PATH:
```bash
make build
./bin/delos prompt list

# Or add to PATH
export PATH=$PATH:$(pwd)/bin
delos prompt list
```

### Rate limiting

**Error:** `rate limit exceeded` inside a `provider_error` message

**Solution:**
- Use a different provider or model
- Add delays/retries in your client
- Check your API quota

## Getting More Help

### Enable debug logging

```bash
export DELOS_LOG_LEVEL=debug
export DELOS_LOG_FORMAT=text   # easier to read than json

# Or in deploy/local/.env.local
```

### Check health

```bash
curl -s localhost:8080/healthz   # gateway + provider count
curl -s localhost:8081/healthz   # control plane

delos gateway health
delos prompt list
```

The control plane also serves gRPC reflection and the standard gRPC health service, so `grpcurl` works:

```bash
grpcurl -plaintext localhost:8081 list
grpcurl -plaintext localhost:8081 grpc.health.v1.Health/Check
```

### View all logs

```bash
make logs

# Specific container
docker compose -f deploy/local/docker-compose.yaml logs -f gateway
docker compose -f deploy/local/docker-compose.yaml logs -f delos
```

### Reset everything

Nuclear option - clean slate:
```bash
# Stop everything
make stop-all
make down

# Remove Docker volumes (deletes all data)
docker compose -f deploy/local/docker-compose.yaml down -v

# Clean build artifacts
make clean

# Rebuild and restart
make build
make up-all
```
