"""Configuration for the Delos SDK."""

from __future__ import annotations

import os
from dataclasses import dataclass, field


@dataclass
class ServiceEndpoint:
    """Configuration for a single service endpoint."""

    host: str = "localhost"
    port: int = 9000
    use_tls: bool = False

    @property
    def address(self) -> str:
        """Return the full address string."""
        return f"{self.host}:{self.port}"


@dataclass
class DelosConfig:
    """Configuration for the Delos SDK.

    Example:
        >>> config = DelosConfig()  # Uses defaults
        >>> config = DelosConfig.from_env()  # Loads from environment
        >>> config = DelosConfig(
        ...     runtime=ServiceEndpoint(port=9001),
        ...     prompt=ServiceEndpoint(port=9002),
        ... )
    """

    observe: ServiceEndpoint = field(default_factory=lambda: ServiceEndpoint(port=9000))
    runtime: ServiceEndpoint = field(default_factory=lambda: ServiceEndpoint(port=9001))
    prompt: ServiceEndpoint = field(default_factory=lambda: ServiceEndpoint(port=9002))
    datasets: ServiceEndpoint = field(default_factory=lambda: ServiceEndpoint(port=9003))
    eval: ServiceEndpoint = field(default_factory=lambda: ServiceEndpoint(port=9004))
    deploy: ServiceEndpoint = field(default_factory=lambda: ServiceEndpoint(port=9005))

    # Authentication
    api_key: str | None = None

    # Timeouts (in seconds)
    timeout: float = 30.0
    connect_timeout: float = 10.0

    @classmethod
    def from_env(cls) -> DelosConfig:
        """Create configuration from environment variables.

        Environment variables:
            DELOS_HOST: Default host for all services
            DELOS_OBSERVE_HOST, DELOS_OBSERVE_PORT: Observe service
            DELOS_RUNTIME_HOST, DELOS_RUNTIME_PORT: Runtime service
            DELOS_PROMPT_HOST, DELOS_PROMPT_PORT: Prompt service
            DELOS_DATASETS_HOST, DELOS_DATASETS_PORT: Datasets service
            DELOS_EVAL_HOST, DELOS_EVAL_PORT: Eval service
            DELOS_DEPLOY_HOST, DELOS_DEPLOY_PORT: Deploy service
            DELOS_API_KEY: API key for authentication
            DELOS_TIMEOUT: Request timeout in seconds
            DELOS_USE_TLS: Whether to use TLS (true/false)
        """
        default_host = os.getenv("DELOS_HOST", "localhost")
        use_tls = os.getenv("DELOS_USE_TLS", "false").lower() == "true"

        def get_endpoint(name: str, default_port: int) -> ServiceEndpoint:
            return ServiceEndpoint(
                host=os.getenv(f"DELOS_{name.upper()}_HOST", default_host),
                port=int(os.getenv(f"DELOS_{name.upper()}_PORT", str(default_port))),
                use_tls=use_tls,
            )

        return cls(
            observe=get_endpoint("observe", 9000),
            runtime=get_endpoint("runtime", 9001),
            prompt=get_endpoint("prompt", 9002),
            datasets=get_endpoint("datasets", 9003),
            eval=get_endpoint("eval", 9004),
            deploy=get_endpoint("deploy", 9005),
            api_key=os.getenv("DELOS_API_KEY"),
            timeout=float(os.getenv("DELOS_TIMEOUT", "30.0")),
            connect_timeout=float(os.getenv("DELOS_CONNECT_TIMEOUT", "10.0")),
        )
