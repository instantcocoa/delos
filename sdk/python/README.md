# Delos Python SDK

Python SDK for the Delos LLM Infrastructure Platform.

## Installation

```bash
pip install delos-sdk
```

Or for development:

```bash
cd sdk/python
pip install -e ".[dev]"
```

## Quick Start

```python
from delos import DelosClient, DelosConfig

# Create client with default config (localhost)
client = DelosClient()

# Or configure from environment variables
config = DelosConfig.from_env()
client = DelosClient(config)

# Use as context manager for automatic cleanup
with DelosClient() as client:
    # Create a prompt
    prompt = client.prompts.create(
        "summarizer",
        template="Summarize the following text:\n\n{{text}}\n\nSummary:",
        description="Summarizes long text into key points",
    )

    # Generate a completion
    from delos.models import Message
    response = client.runtime.complete(
        messages=[Message(role="user", content="Hello, how are you?")],
        model="gpt-4",
    )
    print(response.content)
```

## Services

### Prompts

```python
# Create a prompt
prompt = client.prompts.create(
    "my-prompt",
    template="Hello {{name}}!",
    variables=[PromptVariable(name="name", required=True)],
)

# Get by ID or slug
prompt = client.prompts.get("my-prompt")
prompt = client.prompts.get("my-prompt:v2")  # specific version

# Update (creates new version)
prompt = client.prompts.update(
    prompt.id,
    template="Hi {{name}}!",
    commit_message="Changed greeting",
)

# List prompts
prompts, total = client.prompts.list(tags=["production"])

# Get version history
versions = client.prompts.list_versions(prompt.id)
```

### Runtime

```python
from delos.models import Message, RoutingStrategy

# Generate completion
response = client.runtime.complete(
    messages=[
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content="What is Python?"),
    ],
    model="gpt-4",
    temperature=0.7,
)

# Stream completion
for chunk in client.runtime.complete_stream(
    messages=[Message(role="user", content="Write a poem")],
):
    print(chunk, end="")

# List available models
models = client.runtime.list_models(provider="openai")
```

### Datasets

```python
from delos.models import ExampleInput, ExampleSource

# Create dataset
dataset = client.datasets.create(
    "test-cases",
    prompt_id=prompt.id,
    description="Test cases for summarizer",
)

# Add examples
examples, count = client.datasets.add_examples(
    dataset.id,
    [
        ExampleInput(
            input={"text": "Long article..."},
            expected_output={"summary": "Key points..."},
            source=ExampleSource.MANUAL,
        ),
    ],
)

# Get examples
examples, total = client.datasets.get_examples(
    dataset.id,
    limit=50,
    shuffle=True,
)
```

### Eval

```python
from delos.models import EvalConfig, EvaluatorConfig

# Create evaluation run
run = client.eval.create_run(
    "regression-test",
    prompt_id=prompt.id,
    dataset_id=dataset.id,
    config=EvalConfig(
        evaluators=[
            EvaluatorConfig(type="semantic_similarity", weight=1.0),
            EvaluatorConfig(type="llm_judge", params={"criteria": "accuracy"}),
        ],
        concurrency=5,
    ),
)

# Check status
run = client.eval.get_run(run.id)
print(f"Progress: {run.progress}%")

# Get results
results, total = client.eval.get_results(run.id, failed_only=True)

# Compare runs
run_a, run_b, examples = client.eval.compare_runs(old_run.id, new_run.id)
print(f"Score diff: {run_b.overall_score - run_a.overall_score}")
```

### Deploy

```python
from delos.models import DeploymentStrategy, DeploymentType, GateCondition

# Create quality gate
gate = client.deploy.create_quality_gate(
    "min-score",
    prompt.id,
    conditions=[
        GateCondition(type="eval_score", operator="gte", threshold=0.8),
    ],
)

# Create deployment
deployment = client.deploy.create(
    prompt.id,
    to_version=2,
    environment="production",
    strategy=DeploymentStrategy(
        type=DeploymentType.CANARY,
        initial_percentage=10,
        increment=10,
        interval_seconds=300,
        auto_rollback=True,
        rollback_threshold=0.7,
    ),
)

# Approve deployment
deployment = client.deploy.approve(deployment.id, comment="LGTM")

# Check status
status, rollout, gates = client.deploy.get_status(deployment.id)

# Rollback if needed
original, rollback = client.deploy.rollback(deployment.id, reason="Quality degraded")
```

## Configuration

### Environment Variables

```bash
# Default host for all services
export DELOS_HOST=localhost

# Per-service configuration
export DELOS_RUNTIME_HOST=runtime.example.com
export DELOS_RUNTIME_PORT=9001

# Authentication
export DELOS_API_KEY=your-api-key

# Timeouts
export DELOS_TIMEOUT=30
export DELOS_USE_TLS=true
```

### Programmatic Configuration

```python
from delos import DelosConfig
from delos.config import ServiceEndpoint

config = DelosConfig(
    runtime=ServiceEndpoint(host="runtime.example.com", port=9001, use_tls=True),
    prompt=ServiceEndpoint(host="prompt.example.com", port=9002, use_tls=True),
    timeout=60.0,
)
```

## Development

```bash
# Install dev dependencies
pip install -e ".[dev]"

# Run tests
pytest

# Type checking
mypy src/delos

# Linting
ruff check src/delos
```
