# Quickstart

Get up and running with Delos in 5 minutes.

Delos is two binaries: `delos-gateway` (the data plane, :8080) and `delos` (the
control plane + CLI, :8081).

## 0. The 30-second version

If all you want is a completion through the gateway, you need one container and
one provider key - no clone, no config file, no database:

```bash
docker run -p 8080:8080 -e OPENAI_API_KEY=sk-... ghcr.io/instantcocoa/delos-gateway
```

```python
from openai import OpenAI

client = OpenAI(base_url="http://localhost:8080/v1", api_key="delos-dev")
print(client.chat.completions.create(
    model="gpt-4o-mini",
    messages=[{"role": "user", "content": "Hello!"}],
).choices[0].message.content)
```

In this mode any `api_key` is accepted, models pass straight through to whatever
keys are in the environment, and nothing is metered. Everything below adds the
parts you only need later.

## 1. Configure Your Provider Keys

```bash
git clone https://github.com/instantcocoa/delos.git
cd delos

cp deploy/local/.env.example deploy/local/.env

# Secrets go in deploy/local/.env.local (gitignored)
echo 'OPENAI_API_KEY=sk-...' >> deploy/local/.env.local
```

## 2. Start the Stack

```bash
# postgres + gateway (8080) + control plane (8081)
make up-all
```

Prefer local binaries? `make up && make build && make run-all`.

Verify:

```bash
curl http://localhost:8080/healthz    # {"status":"ok","providers":1}
curl http://localhost:8081/healthz    # {"status":"ok"}
```

## 3. Run Your First Completion

The gateway is OpenAI-compatible, so curl works directly:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [{"role": "user", "content": "Summarize the Odyssey in one line."}]
  }'
```

Or with the CLI:

```bash
./bin/delos gateway models
./bin/delos gateway complete "Summarize the Odyssey in one line." --model gpt-4o-mini
```

Or from your application - no Delos SDK required:

```python
from openai import OpenAI

client = OpenAI(base_url="http://localhost:8080/v1", api_key="delos-dev")
response = client.chat.completions.create(
    model="gpt-4o-mini",
    messages=[{"role": "user", "content": "Summarize the Odyssey in one line."}],
)
print(response.choices[0].message.content)
```

That is the whole data plane. Everything below is optional.

## 4. Watch the traffic

```bash
./bin/delos tail
```

```
TIME      STATUS  MODEL         PROVIDER   LATENCY  TOKENS   COST      CACHE  KEY
14:22:03  200     gpt-4o-mini   openai       412ms  842->96  $0.00013  -      -
14:22:04  200     gpt-4o-mini   openai       188us  842->96  $0.00013  hit    -
```

Run a completion in another terminal and it appears immediately. `--replay 20`
shows recent history first, `--json` emits raw events for scripting. This is the
fastest way to confirm your app is actually going through Delos.

## 5. Check your setup

```bash
./bin/delos config doctor
```

It probes the control plane, the gateway, PostgreSQL (when configured) and
provider connectivity, then exits 1 if anything failed - so it doubles as a CI
smoke test. To check a config file without touching the network:

```bash
# docs-test
cp delos.yaml.example delos.yaml
delos config validate delos.yaml
```

## 6. Lock the gateway down with virtual keys

Once the gateway has Postgres (`DELOS_STORAGE_BACKEND=postgres`), every request
needs a key:

```bash
./bin/delos key create quickstart --usd-budget 10
# dk_... shown once - store it now
```

```python
client = OpenAI(base_url="http://localhost:8080/v1", api_key="dk_...")
```

Over-budget requests are refused with a 429 before reaching a provider, and you
will see the refusal in `delos tail`.

## 7. Create a Prompt

```bash
./bin/delos prompt create "Summarizer" \
  --slug summarizer \
  --system "Summarize the following text concisely."

./bin/delos prompt list
```

Updating a prompt creates a new version automatically:

```bash
./bin/delos prompt update <prompt-id> \
  --system "Summarize the following text in exactly 3 bullet points." \
  --change-description "Switch to bullet points"

./bin/delos prompt history <prompt-id>
```

Prompt IDs look like `pmt_1730000000000000000`; dataset, eval-run, and
deployment IDs are UUIDs.

Prompts are files first - the control plane is an index, your repo is the source
of truth:

```bash
./bin/delos prompt pull ./prompts        # export every prompt as .prompt.yaml
$EDITOR ./prompts/summarizer.prompt.yaml
./bin/delos prompt diff ./prompts        # semantic diff against the server
./bin/delos prompt push ./prompts        # sync back (--dry-run to preview)
```

## 8. Create a Test Dataset

```bash
./bin/delos datasets create "Summarization Tests" --prompt <prompt-id>
./bin/delos datasets list
./bin/delos datasets examples <dataset-id>
```

## 9. Run an Evaluation

```bash
./bin/delos eval evaluators
./bin/delos eval run --prompt <prompt-id> --dataset <dataset-id> \
  --model gpt-4o-mini --evaluators exact_match   # --model is required
./bin/delos eval list
./bin/delos eval results <run-id>
```

Evaluations get their completions from the gateway over its OpenAI-compatible
API, using `DELOS_GATEWAY_URL`.

## 10. Gate a merge on the results

Delos does not deploy anything for you; it produces a verdict your CI acts on.

```bash
./bin/delos gate create nightly --prompt <prompt-id> --condition 'overall_score>=0.8'
./bin/delos gate check nightly     # exit 0 = pass, 1 = fail
```

Put `delos gate check nightly` in a CI step and the pipeline blocks when quality
regresses.

## Next Steps

- [Configuration Reference](configuration.md) - `delos.yaml` and every environment variable
- [Providers](../providers.md) - Per-provider setup and the feature matrix
- [OpenTelemetry](../otel.md) - Send traces to Jaeger, Grafana or Datadog
- [Deploying](../deploy.md) - TLS, Postgres sizing, HA
- [API Reference](../api-reference/runtime.md) - Full API documentation
