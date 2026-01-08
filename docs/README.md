# Delos Documentation

Delos is a unified infrastructure platform for LLM applications, providing prompt versioning, evaluation, safe deployment, and observability.

## Quick Links

### Getting Started
- [Installation](getting-started/installation.md) - Setup and dependencies
- [Quickstart](getting-started/quickstart.md) - 5-minute tutorial
- [Configuration](getting-started/configuration.md) - Environment variables reference

### Architecture
- [Overview](architecture/overview.md) - System design and components
- [Services](architecture/services.md) - The 6 microservices explained

### API Reference
- [Runtime Service](api-reference/runtime.md) - LLM gateway (5 endpoints)
- [Prompt Service](api-reference/prompt.md) - Prompt versioning (8 endpoints)
- [Datasets Service](api-reference/datasets.md) - Test data management (10 endpoints)
- [Eval Service](api-reference/eval.md) - Quality assurance (8 endpoints)
- [Deploy Service](api-reference/deploy.md) - Deployment orchestration (10 endpoints)
- [Observe Service](api-reference/observe.md) - Tracing and metrics (5 endpoints)

### Guides
- [LLM Providers](guides/providers.md) - Configure OpenAI, Anthropic, Gemini, Ollama
- [Prompt Management](guides/prompts.md) - Versioning workflow
- [Running Evaluations](guides/evaluation.md) - Quality testing
- [Safe Deployments](guides/deployment.md) - Rollout strategies

### SDK
- [Python SDK](sdk/python.md) - Client library reference

## Service Ports

| Service | Port | Purpose |
|---------|------|---------|
| observe | 9000 | Tracing and metrics |
| runtime | 9001 | LLM gateway |
| prompt | 9002 | Prompt versioning |
| datasets | 9003 | Test data management |
| eval | 9004 | Quality evaluation |
| deploy | 9005 | Deployment orchestration |

## Quick Example

```python
from delos import DelosClient

async with DelosClient() as client:
    # Create a versioned prompt
    prompt = await client.prompts.create(
        name="summarizer",
        slug="summarizer",
        messages=[{"role": "system", "content": "Summarize the text."}]
    )

    # Run an LLM completion
    response = await client.runtime.complete(
        prompt_ref=f"{prompt.slug}:v{prompt.version}",
        variables={"text": "Long article here..."}
    )

    print(response.content)
```

## Getting Help

- [GitHub Issues](https://github.com/your-org/delos/issues) - Bug reports and feature requests
- [Discussions](https://github.com/your-org/delos/discussions) - Questions and community
