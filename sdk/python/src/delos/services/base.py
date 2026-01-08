"""Base client for gRPC services."""

from __future__ import annotations

from typing import TYPE_CHECKING

import grpc

if TYPE_CHECKING:
    from delos.config import ServiceEndpoint


class BaseClient:
    """Base class for service clients."""

    def __init__(self, endpoint: ServiceEndpoint, timeout: float = 30.0) -> None:
        """Initialize the client.

        Args:
            endpoint: Service endpoint configuration.
            timeout: Default timeout for requests in seconds.
        """
        self._endpoint = endpoint
        self._timeout = timeout
        self._channel: grpc.Channel | None = None

    @property
    def channel(self) -> grpc.Channel:
        """Get or create the gRPC channel."""
        if self._channel is None:
            if self._endpoint.use_tls:
                credentials = grpc.ssl_channel_credentials()
                self._channel = grpc.secure_channel(
                    self._endpoint.address,
                    credentials,
                )
            else:
                self._channel = grpc.insecure_channel(self._endpoint.address)
        return self._channel

    def close(self) -> None:
        """Close the gRPC channel."""
        if self._channel is not None:
            self._channel.close()
            self._channel = None

    def __enter__(self) -> "BaseClient":
        """Context manager entry."""
        return self

    def __exit__(self, *args: object) -> None:
        """Context manager exit."""
        self.close()
