# Installation

Delos ships as **two binaries plus PostgreSQL**:

| Binary | Role | Port |
|--------|------|------|
| `delos-gateway` | Data plane - OpenAI/Anthropic-compatible LLM gateway. Stateless, no database. | 8080 |
| `delos` | Control plane + CLI. `delos serve` runs the control plane. | 8081 |

## Prerequisites

- **Go 1.25+** - For building from source
- **Docker** and **Docker Compose** - For the local development stack
- **Buf** - Only needed if you change proto definitions (`make tools`)

## Option 0: `go install`

Both binaries install with the Go toolchain, no clone required:

```bash
go install github.com/instantcocoa/delos/cmd/delos-gateway@latest
go install github.com/instantcocoa/delos/cmd/delos@latest
```

They land in `$(go env GOPATH)/bin`. Verify:

```bash
# docs-test
delos version
delos --help
```

Tagged releases also publish signed archives for linux and darwin on amd64 and
arm64, built with `CGO_ENABLED=0`: each release carries an SBOM and a
cosign-signed checksum file. Verify a download with:

```bash
cosign verify-blob checksums.txt \
  --signature checksums.txt.sig \
  --certificate checksums.txt.pem \
  --certificate-identity-regexp 'https://github.com/instantcocoa/delos/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum -c checksums.txt --ignore-missing
```

## Clone the Repository

```bash
git clone https://github.com/instantcocoa/delos.git
cd delos
```

## Option 1: Build from Source

```bash
# Builds bin/delos and bin/delos-gateway
make build

# Or one at a time
make build-delos
make build-gateway

# Or directly with go
go build -o bin/delos ./cmd/delos
go build -o bin/delos-gateway ./cmd/delos-gateway
```

Put the CLI on your PATH if you like:

```bash
sudo mv bin/delos /usr/local/bin/
delos --help
delos version
```

Run the binaries:

```bash
# PostgreSQL only - that's all Delos needs
make up

# Both binaries in the background (make stop-all to stop them)
make run-all

# Or one in the foreground each
make run-gateway        # :8080
make run-control-plane  # :8081
```

## Option 2: Docker Compose

The local stack is three containers: postgres, gateway, control plane.

```bash
cp deploy/local/.env.example deploy/local/.env
# Put your API keys in deploy/local/.env.local (gitignored)

make up-all
# Equivalent to:
#   docker compose -f deploy/local/docker-compose.yaml up -d
```

Optional profiles, not started by default: `ollama` (local models on 11434),
`observability` (Jaeger UI on 16686), `test` (LocalStack on 4566).

```bash
make up-ollama
docker compose -f deploy/local/docker-compose.yaml --profile observability up -d
```

## Option 3: Prebuilt Docker Images

CI publishes both images from the root `Dockerfile.delos` and `Dockerfile.gateway`:

```bash
docker pull ghcr.io/instantcocoa/delos-delos:latest
docker pull ghcr.io/instantcocoa/delos-delos-gateway:latest
```

The gateway needs nothing but provider keys:

```bash
docker run -p 8080:8080 \
  -e OPENAI_API_KEY=sk-... \
  ghcr.io/instantcocoa/delos-delos-gateway:latest
```

The control-plane image defaults to `serve`:

```bash
docker run -p 8081:8081 \
  -e DELOS_STORAGE_BACKEND=postgres \
  -e DELOS_DB_HOST=postgres \
  -e DELOS_GATEWAY_URL=http://gateway:8080 \
  ghcr.io/instantcocoa/delos-delos:latest
```

The control plane applies its own database migrations at startup - there is no
separate migrate step.

## Kubernetes

Kubernetes manifests are being redone for the two-binary architecture and are
not available yet.

## Verify Installation

```bash
curl http://localhost:8080/healthz    # gateway: {"status":"ok","providers":N}
curl http://localhost:8081/healthz    # control plane: {"status":"ok"}

delos gateway health
delos gateway models
delos prompt list
```

Or let the CLI check everything at once - config file, control plane, gateway,
PostgreSQL and provider connectivity:

```bash
delos config doctor
```

It exits 1 if any check fails and prints what to do about each failure.

## Using the Gateway from Your Application

There is no Delos SDK. The gateway speaks the OpenAI and Anthropic HTTP APIs, so
point any existing client at it:

```python
from openai import OpenAI

client = OpenAI(base_url="http://localhost:8080/v1", api_key="delos-dev")
```

```python
import anthropic

client = anthropic.Anthropic(base_url="http://localhost:8080", api_key="delos-dev")
```

## LLM Provider Setup

The gateway registers one provider for every key it finds in the environment:

```bash
export OPENAI_API_KEY=sk-...
export ANTHROPIC_API_KEY=sk-ant-...
export GEMINI_API_KEY=AIza...          # or GOOGLE_API_KEY

# Ollama (local models)
export DELOS_RUNTIME_OLLAMA_ENABLED=true
export DELOS_RUNTIME_OLLAMA_URL=http://localhost:11434
```

With no keys set, the gateway still starts and logs a warning; completions fail
until a provider is configured.

See [Configuration](configuration.md) for all available options.
