"""Client for the runtime service."""

from __future__ import annotations

import sys
from collections.abc import Iterator
from typing import TYPE_CHECKING

from delos.models.runtime import (
    CompletionParams,
    CompletionResponse,
    Message,
    Model,
    Provider,
    RoutingStrategy,
    Usage,
)
from delos.services.base import BaseClient

if TYPE_CHECKING:
    from delos.config import ServiceEndpoint

sys.path.insert(0, "gen/python")

try:
    from runtime.v1 import runtime_pb2, runtime_pb2_grpc
except ImportError:
    runtime_pb2 = None  # type: ignore
    runtime_pb2_grpc = None  # type: ignore


class RuntimeClient(BaseClient):
    """Client for the runtime service."""

    def __init__(self, endpoint: ServiceEndpoint, timeout: float = 30.0) -> None:
        """Initialize the runtime client."""
        super().__init__(endpoint, timeout)
        self._stub: runtime_pb2_grpc.RuntimeServiceStub | None = None

    @property
    def stub(self) -> runtime_pb2_grpc.RuntimeServiceStub:
        """Get the gRPC stub."""
        if self._stub is None:
            if runtime_pb2_grpc is None:
                raise ImportError("Generated protobuf code not found. Run 'buf generate' first.")
            self._stub = runtime_pb2_grpc.RuntimeServiceStub(self.channel)
        return self._stub

    def complete(
        self,
        messages: list[Message] | None = None,
        *,
        model: str = "",
        system_prompt: str = "",
        max_tokens: int = 1024,
        temperature: float = 0.7,
        top_p: float = 1.0,
        stop_sequences: list[str] | None = None,
        provider: str = "",
        routing_strategy: RoutingStrategy = RoutingStrategy.UNSPECIFIED,
        metadata: dict[str, str] | None = None,
    ) -> CompletionResponse:
        """Generate a completion.

        Args:
            messages: Conversation messages.
            model: Model to use.
            system_prompt: System prompt.
            max_tokens: Maximum tokens to generate.
            temperature: Sampling temperature.
            top_p: Top-p sampling.
            stop_sequences: Stop sequences.
            provider: Specific provider to use.
            routing_strategy: Routing strategy.
            metadata: Request metadata.

        Returns:
            The completion response.
        """
        pb_messages = []
        if messages:
            for m in messages:
                pb_messages.append(runtime_pb2.Message(role=m.role, content=m.content))

        strategy_map = {
            RoutingStrategy.UNSPECIFIED: runtime_pb2.ROUTING_STRATEGY_UNSPECIFIED,
            RoutingStrategy.COST: runtime_pb2.ROUTING_STRATEGY_COST,
            RoutingStrategy.LATENCY: runtime_pb2.ROUTING_STRATEGY_LATENCY,
            RoutingStrategy.QUALITY: runtime_pb2.ROUTING_STRATEGY_QUALITY,
        }

        params = runtime_pb2.CompletionParams(
            model=model,
            messages=pb_messages,
            system_prompt=system_prompt,
            max_tokens=max_tokens,
            temperature=temperature,
            top_p=top_p,
            stop_sequences=stop_sequences or [],
            provider=provider,
            routing_strategy=strategy_map.get(
                routing_strategy, runtime_pb2.ROUTING_STRATEGY_UNSPECIFIED
            ),
            metadata=metadata or {},
        )

        request = runtime_pb2.CompleteRequest(params=params)
        response = self.stub.Complete(request, timeout=self._timeout)
        return self._to_response(response)

    def complete_stream(
        self,
        messages: list[Message] | None = None,
        *,
        model: str = "",
        system_prompt: str = "",
        max_tokens: int = 1024,
        temperature: float = 0.7,
        **kwargs: object,
    ) -> Iterator[str]:
        """Generate a streaming completion.

        Yields:
            Content chunks as they are generated.
        """
        pb_messages = []
        if messages:
            for m in messages:
                pb_messages.append(runtime_pb2.Message(role=m.role, content=m.content))

        params = runtime_pb2.CompletionParams(
            model=model,
            messages=pb_messages,
            system_prompt=system_prompt,
            max_tokens=max_tokens,
            temperature=temperature,
        )

        request = runtime_pb2.CompleteStreamRequest(params=params)
        for response in self.stub.CompleteStream(request, timeout=self._timeout):
            if response.content:
                yield response.content

    def list_models(self, provider: str = "") -> list[Model]:
        """List available models.

        Args:
            provider: Filter by provider (optional).

        Returns:
            List of available models.
        """
        request = runtime_pb2.ListModelsRequest(provider=provider)
        response = self.stub.ListModels(request, timeout=self._timeout)
        return [
            Model(
                id=m.id,
                name=m.name,
                provider=m.provider,
                context_window=m.context_window,
                max_output_tokens=m.max_output_tokens,
                supports_vision=m.supports_vision,
                supports_function_calling=m.supports_function_calling,
                cost_per_input_token=m.cost_per_input_token,
                cost_per_output_token=m.cost_per_output_token,
            )
            for m in response.models
        ]

    def list_providers(self) -> list[Provider]:
        """List available providers.

        Returns:
            List of available providers.
        """
        request = runtime_pb2.ListProvidersRequest()
        response = self.stub.ListProviders(request, timeout=self._timeout)
        return [
            Provider(
                id=p.id,
                name=p.name,
                models=[
                    Model(
                        id=m.id,
                        name=m.name,
                        provider=m.provider,
                    )
                    for m in p.models
                ],
                is_available=p.is_available,
            )
            for p in response.providers
        ]

    def _to_response(self, pb: runtime_pb2.CompleteResponse) -> CompletionResponse:
        """Convert protobuf to model."""
        return CompletionResponse(
            id=pb.id,
            content=pb.content,
            model=pb.model,
            provider=pb.provider,
            usage=Usage(
                prompt_tokens=pb.usage.prompt_tokens,
                completion_tokens=pb.usage.completion_tokens,
                total_tokens=pb.usage.total_tokens,
            ),
            latency_ms=pb.latency_ms,
            finish_reason=pb.finish_reason,
            metadata=dict(pb.metadata),
        )
