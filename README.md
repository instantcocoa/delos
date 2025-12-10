# 🌌 Delos: The Master Control System for LLM Applications

**Delos** is the unified, open-source infrastructure platform that makes LLM applications reliable, testable, and safe to deploy at scale. It acts as the single Control Plane for the entire development lifecycle, solving the critical gaps left by fragmented point solutions.

## 🚀 The Core Problem Delos Solves

Developers shipping LLM applications face constant chaos: Foundation Models (GPT, Claude) update frequently and silently break prompts; testing is manual; costs spiral; and deployments are terrifying due to the risk of regression.

Delos ensures **reliability in a state of flux** by integrating:

1.  **Model Version Control:** Automatically tests your prompts when foundation models update.
2.  **Quality Gates:** Blocks deployments if quality metrics drop below an acceptable threshold.
3.  **Active Optimization:** Intelligently routes calls to minimize cost and latency.

## ✨ The Delos Experience: A Unified Control Plane

Delos is built as a set of high-performance **Go microservices** communicating via **gRPC**, providing a fast, stable backend for all cross-language clients.

### 1. The SDK (`delos` library)

The simplest, most reliable way to interact with LLMs.

> **Example:** Reliable calls, type-safe output, and automatic tracking.
> ```python
> from delos import LLM
> from pydantic import BaseModel
> 
> class Summary(BaseModel):
>     summary: str
> 
> llm = LLM(routing="cost_optimized")
> 
> # 1. Use a versioned prompt from the Prompt Store
> result = llm.use("summarizer:v2.1", text=document, output=Summary) 
> 
> # 2. All calls are automatically tracked by the Observability Service
> # 3. Output is validated and converted into a type-safe object
> ```

### 2. The CLI (`delos` command)

The **Control Room** for your entire LLM operation.

* **Test Regression:** `delos test model-regression --new gpt-4.1 --datasets all`
* **Deploy Safely:** `delos deploy api-service --strategy gradual --rollback-if-degraded`
* **Dataset Mgmt:** `delos datasets auto-generate`

## 🏗️ Architectural Overview: 6 Go Services

Delos is structured as six independent, composable microservices that replace the fragmented ecosystem: 

| Service | Go Project | Role & Focus |
| :--- | :--- | :--- |
| **Runtime** | `delos-core-runtime` | **LLM Gateway** (Replacing LiteLLM): Provider abstraction, semantic caching, failovers. |
| **Prompt** | `delos-prompt-store` | **Prompt Git** (Replacing Config Files): Versioning, collaboration, semantic diffing. |
| **Datasets** | `delos-datasets-manager` | **Living Golden Data** (Replacing Manual Curation): Auto-generates, versions, and analyzes test suites. |
| **Eval** | `delos-eval-engine` | **Quality Assurance** (Replacing Custom Scripts): Regression testing, semantic evaluators, quality scoring. |
| **Deploy** | `delos-deploy-system` | **CI/CD Gates** (Replacing Manual Deploy): Safe rollouts, A/B testing, auto-rollback based on quality. |
| **Observe** | `delos-observability` | **Tracing Backend** (Replacing Langfuse): Ingests OTLP traces and provides the data engine for all services. |

## 🤝 Getting Started

We recommend setting up the core services locally using Docker Compose, starting with the Runtime and Prompt services.

1.  **Clone the Architecture:**
    ```bash
    git clone [https://github.com/instantcocoa/delos-platform.git](https://github.com/instantcocoa/delos-platform.git)
    cd delos-platform
    ```

2.  **Set Up Local Go Repositories:**
    * Create separate repositories under **instantcocoa** for each Go service (e.g., `delos-core-runtime`, `delos-prompt-store`).

3.  **Install the CLI:**
    ```bash
    # Build and install the CLI from the delos-cli repository
    # You will need Go installed
    go install [github.com/instantcocoa/delos-cli@latest](https://github.com/instantcocoa/delos-cli@latest)
    ```

4.  **Run the Services:**
    *(Placeholder for future `docker-compose up` instruction)*
    ```bash
    docker-compose -f deploy/local/docker-compose.yaml up -d
    ```

## 📜 Contributing

Delos is built on the philosophy of superior engineering and open contribution. We welcome contributions, especially to the core Go services and client SDKs.

* Read our [CONTRIBUTING.md] for guidelines.
* Check the [ARCHITECTURE.md] for deep dives into the gRPC protocols.
* Join the discussion on our [Discord/Slack].

**Delos: The infrastructure that ensures your LLM applications are reliable, testable, and safe to ship.**
