# Roadmap

Deliberate non-goals for now, revisited as demand proves out:

- **Semantic caching.** The gateway caches exact-match requests only
  (same model, normalized messages, and parameters). Similarity-based caching
  adds an embedding dependency to the hot path and a correctness risk that is
  not worth it yet.
- **ClickHouse trace storage.** Observe stores spans in Postgres. If ingest
  volume outgrows it, the answer is documented ClickHouse support, not a
  custom store.
- **Provider long tail.** Delos supports a curated provider set (OpenAI,
  Anthropic, Google Gemini, AWS Bedrock, and any OpenAI-compatible endpoint)
  well, rather than a hundred providers badly. The OpenAI-compatible provider
  covers vLLM, SGLang, Ollama, Together, OpenRouter, and most gateways.
