"""Common models used across services."""

from __future__ import annotations

from typing import Generic, TypeVar

from pydantic import BaseModel

T = TypeVar("T")


class Metadata(BaseModel):
    """Generic metadata dictionary."""

    data: dict[str, str] = {}


class PaginatedResponse(BaseModel, Generic[T]):
    """Generic paginated response."""

    items: list[T]
    total_count: int
    limit: int
    offset: int

    @property
    def has_more(self) -> bool:
        """Check if there are more items available."""
        return self.offset + len(self.items) < self.total_count
