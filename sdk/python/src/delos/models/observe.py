"""Models for the observe service."""

from __future__ import annotations

from datetime import datetime
from enum import Enum

from pydantic import BaseModel


class SpanKind(str, Enum):
    """Kind of span."""

    UNSPECIFIED = "unspecified"
    INTERNAL = "internal"
    SERVER = "server"
    CLIENT = "client"
    PRODUCER = "producer"
    CONSUMER = "consumer"


class SpanStatus(str, Enum):
    """Status of a span."""

    UNSET = "unset"
    OK = "ok"
    ERROR = "error"


class Span(BaseModel):
    """A single span in a trace."""

    trace_id: str
    span_id: str
    parent_span_id: str | None = None
    name: str
    kind: SpanKind = SpanKind.INTERNAL
    start_time: datetime
    end_time: datetime | None = None
    status: SpanStatus = SpanStatus.UNSET
    status_message: str | None = None
    attributes: dict[str, str] = {}
    service_name: str = ""

    @property
    def duration_ms(self) -> float | None:
        """Calculate duration in milliseconds."""
        if self.end_time is None:
            return None
        return (self.end_time - self.start_time).total_seconds() * 1000


class Trace(BaseModel):
    """A complete trace with all its spans."""

    trace_id: str
    spans: list[Span] = []
    service_name: str = ""
    start_time: datetime | None = None
    end_time: datetime | None = None

    @property
    def duration_ms(self) -> float | None:
        """Calculate total trace duration in milliseconds."""
        if self.start_time is None or self.end_time is None:
            return None
        return (self.end_time - self.start_time).total_seconds() * 1000

    @property
    def root_span(self) -> Span | None:
        """Get the root span of the trace."""
        for span in self.spans:
            if span.parent_span_id is None:
                return span
        return None
