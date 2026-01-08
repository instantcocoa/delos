"""Main Delos client combining all service clients."""

from __future__ import annotations

from delos.config import DelosConfig
from delos.services.datasets import DatasetsClient
from delos.services.deploy import DeployClient
from delos.services.eval import EvalClient
from delos.services.observe import ObserveClient
from delos.services.prompt import PromptClient
from delos.services.runtime import RuntimeClient


class DelosClient:
    """Main client for the Delos platform.

    Provides access to all Delos services through a unified interface.

    Example:
        >>> from delos import DelosClient, DelosConfig
        >>>
        >>> # Create client with default config
        >>> client = DelosClient()
        >>>
        >>> # Or with custom config
        >>> config = DelosConfig.from_env()
        >>> client = DelosClient(config)
        >>>
        >>> # Use service clients
        >>> prompt = client.prompts.create("summarizer", template="...")
        >>> response = client.runtime.complete(messages=[...])
        >>>
        >>> # Clean up
        >>> client.close()
        >>>
        >>> # Or use as context manager
        >>> with DelosClient() as client:
        ...     prompt = client.prompts.get("summarizer")
    """

    def __init__(self, config: DelosConfig | None = None) -> None:
        """Initialize the Delos client.

        Args:
            config: Configuration for connecting to services.
                    If not provided, uses default configuration.
        """
        self._config = config or DelosConfig()
        self._observe: ObserveClient | None = None
        self._runtime: RuntimeClient | None = None
        self._prompts: PromptClient | None = None
        self._datasets: DatasetsClient | None = None
        self._eval: EvalClient | None = None
        self._deploy: DeployClient | None = None

    @property
    def config(self) -> DelosConfig:
        """Get the client configuration."""
        return self._config

    @property
    def observe(self) -> ObserveClient:
        """Get the observe service client."""
        if self._observe is None:
            self._observe = ObserveClient(
                self._config.observe,
                timeout=self._config.timeout,
            )
        return self._observe

    @property
    def runtime(self) -> RuntimeClient:
        """Get the runtime service client."""
        if self._runtime is None:
            self._runtime = RuntimeClient(
                self._config.runtime,
                timeout=self._config.timeout,
            )
        return self._runtime

    @property
    def prompts(self) -> PromptClient:
        """Get the prompt service client."""
        if self._prompts is None:
            self._prompts = PromptClient(
                self._config.prompt,
                timeout=self._config.timeout,
            )
        return self._prompts

    @property
    def datasets(self) -> DatasetsClient:
        """Get the datasets service client."""
        if self._datasets is None:
            self._datasets = DatasetsClient(
                self._config.datasets,
                timeout=self._config.timeout,
            )
        return self._datasets

    @property
    def eval(self) -> EvalClient:
        """Get the eval service client."""
        if self._eval is None:
            self._eval = EvalClient(
                self._config.eval,
                timeout=self._config.timeout,
            )
        return self._eval

    @property
    def deploy(self) -> DeployClient:
        """Get the deploy service client."""
        if self._deploy is None:
            self._deploy = DeployClient(
                self._config.deploy,
                timeout=self._config.timeout,
            )
        return self._deploy

    def close(self) -> None:
        """Close all service connections."""
        if self._observe is not None:
            self._observe.close()
            self._observe = None
        if self._runtime is not None:
            self._runtime.close()
            self._runtime = None
        if self._prompts is not None:
            self._prompts.close()
            self._prompts = None
        if self._datasets is not None:
            self._datasets.close()
            self._datasets = None
        if self._eval is not None:
            self._eval.close()
            self._eval = None
        if self._deploy is not None:
            self._deploy.close()
            self._deploy = None

    def __enter__(self) -> "DelosClient":
        """Context manager entry."""
        return self

    def __exit__(self, *args: object) -> None:
        """Context manager exit."""
        self.close()

    def health_check(self) -> dict[str, bool]:
        """Check health of all services.

        Returns:
            Dictionary mapping service name to health status.
        """
        results = {}

        services = [
            ("observe", self._config.observe),
            ("runtime", self._config.runtime),
            ("prompt", self._config.prompt),
            ("datasets", self._config.datasets),
            ("eval", self._config.eval),
            ("deploy", self._config.deploy),
        ]

        for name, endpoint in services:
            try:
                # Try to connect and check health
                # This is a simple connectivity check
                import grpc

                channel = grpc.insecure_channel(endpoint.address)
                try:
                    grpc.channel_ready_future(channel).result(timeout=2)
                    results[name] = True
                except grpc.FutureTimeoutError:
                    results[name] = False
                finally:
                    channel.close()
            except Exception:
                results[name] = False

        return results
