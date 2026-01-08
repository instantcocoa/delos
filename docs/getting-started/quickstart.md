# Quickstart

Get up and running with Delos in 5 minutes.

## 1. Start the Services

```bash
# Start infrastructure
docker-compose -f deploy/local/docker-compose.yaml up -d

# Start all Delos services
make run-all
```

## 2. Create Your First Prompt

Using the CLI:

```bash
delos prompt create \
  --name "Summarizer" \
  --slug "summarizer" \
  --message "system:Summarize the following text concisely."
```

Or with Python:

```python
from delos import DelosClient

async with DelosClient() as client:
    prompt = await client.prompts.create(
        name="Summarizer",
        slug="summarizer",
        messages=[
            {"role": "system", "content": "Summarize the following text concisely."}
        ]
    )
    print(f"Created prompt: {prompt.id} (v{prompt.version})")
```

## 3. Run an LLM Completion

Using the CLI:

```bash
delos runtime complete \
  --prompt-ref "summarizer:v1" \
  --variable "text=The quick brown fox jumps over the lazy dog. This sentence contains every letter of the alphabet."
```

Or with Python:

```python
response = await client.runtime.complete(
    prompt_ref="summarizer:v1",
    variables={"text": "Your long text here..."}
)
print(response.content)
```

## 4. Update the Prompt

```bash
delos prompt update \
  --id "pmt_..." \
  --message "system:Summarize the following text in exactly 3 bullet points."
```

This creates version 2 automatically.

## 5. Create a Test Dataset

```bash
# Create a dataset
delos datasets create \
  --name "Summarization Tests" \
  --prompt-id "pmt_..."

# Add test examples
delos datasets add-examples \
  --dataset-id "ds_..." \
  --input '{"text": "Long article..."}' \
  --expected "Short summary"
```

## 6. Run an Evaluation

```bash
delos eval create \
  --prompt-id "pmt_..." \
  --dataset-id "ds_..." \
  --evaluator "semantic_similarity"
```

## 7. Deploy with Approval

```bash
# Create a deployment (requires approval)
delos deploy create \
  --prompt-id "pmt_..." \
  --to-version 2 \
  --environment production

# Approve after review
delos deploy approve --id "dep_..."
```

## Next Steps

- [Configuration Reference](configuration.md) - All environment variables
- [LLM Providers Guide](../guides/providers.md) - Configure multiple providers
- [API Reference](../api-reference/runtime.md) - Full API documentation
