"""Client for the observe service."""

from __future__ import annotations

import sys
from datetime import datetime, timezone
from typing import TYPE_CHECKING

from delos.models.observe import Span, SpanKind, SpanStatus, Trace
from delos.services.base import BaseClient

if TYPE_CHECKING:
    from delos.config import ServiceEndpoint

sys.path.insert(0, "gen/python")

try:
    from observe.v1 import observe_pb2, observe_pb2_grpc
except ImportError:
    observe_pb2 = None  # type: ignore
    observe_pb2_grpc = None  # type: ignore


class ObserveClient(BaseClient):
    """Client for the observe service."""

    def __init__(self, endpoint: ServiceEndpoint, timeout: float = 30.0) -> None:
        """Initialize the observe client."""
        super().__init__(endpoint, timeout)
        self._stub: observe_pb2_grpc.ObserveServiceStub | None = None

    @property
    def stub(self) -> observe_pb2_grpc.ObserveServiceStub:
        """Get the gRPC stub."""
        if self._stub is None:
            if observe_pb2_grpc is None:
                raise ImportError("Generated protobuf code not found. Run 'buf generate' first.")
            self._stub = observe_pb2_grpc.ObserveServiceStub(self.channel)
        return self._stub

    def ingest_spans(self, spans: list[Span]) -> int:
        """Ingest spans into the observe service.

        Args:
            spans: List of spans to ingest.

        Returns:
            Number of spans accepted.
        """
        pb_spans = []
        for span in spans:
            pb_span = observe_pb2.Span(
                trace_id=span.trace_id,
                span_id=span.span_id,
                parent_span_id=span.parent_span_id or "",
                name=span.name,
                kind=self._kind_to_pb(span.kind),
                status=self._status_to_pb(span.status),
                status_message=span.status_message or "",
                attributes=span.attributes,
                service_name=span.service_name,
            )
            if span.start_time:
                pb_span.start_time.FromDatetime(span.start_time)
            if span.end_time:
                pb_span.end_time.FromDatetime(span.end_time)
            pb_spans.append(pb_span)

        request = observe_pb2.IngestSpansRequest(spans=pb_spans)
        response = self.stub.IngestSpans(request, timeout=self._timeout)
        return response.accepted_count

    def get_trace(self, trace_id: str) -> Trace | None:
        """Get a trace by ID.

        Args:
            trace_id: The trace ID.

        Returns:
            The trace if found, None otherwise.
        """
        request = observe_pb2.GetTraceRequest(trace_id=trace_id)
        try:
            response = self.stub.GetTrace(request, timeout=self._timeout)
            return self._to_trace(response.trace)
        except Exception:
            return None

    def query_traces(
        self,
        *,
        service_name: str = "",
        start_time: datetime | None = None,
        end_time: datetime | None = None,
        limit: int = 100,
    ) -> list[Trace]:
        """Query traces.

        Args:
            service_name: Filter by service name.
            start_time: Start of time range.
            end_time: End of time range.
            limit: Maximum number of traces.

        Returns:
            List of matching traces.
        """
        request = observe_pb2.QueryTracesRequest(
            service_name=service_name,
            limit=limit,
        )
        if start_time:
            request.start_time.FromDatetime(start_time)
        if end_time:
            request.end_time.FromDatetime(end_time)

        response = self.stub.QueryTraces(request, timeout=self._timeout)
        return [self._to_trace(t) for t in response.traces]

    def _to_trace(self, pb: observe_pb2.Trace) -> Trace:
        """Convert protobuf to model."""
        spans = [self._to_span(s) for s in pb.spans]
        return Trace(
            trace_id=pb.trace_id,
            spans=spans,
            service_name=pb.service_name,
            start_time=pb.start_time.ToDatetime(timezone.utc) if pb.HasField("start_time") else None,
            end_time=pb.end_time.ToDatetime(timezone.utc) if pb.HasField("end_time") else None,
        )

    def _to_span(self, pb: observe_pb2.Span) -> Span:
        """Convert protobuf span to model."""
        return Span(
            trace_id=pb.trace_id,
            span_id=pb.span_id,
            parent_span_id=pb.parent_span_id if pb.parent_span_id else None,
            name=pb.name,
            kind=self._kind_from_pb(pb.kind),
            start_time=pb.start_time.ToDatetime(timezone.utc),
            end_time=pb.end_time.ToDatetime(timezone.utc) if pb.HasField("end_time") else None,
            status=self._status_from_pb(pb.status),
            status_message=pb.status_message if pb.status_message else None,
            attributes=dict(pb.attributes),
            service_name=pb.service_name,
        )

    def _kind_to_pb(self, kind: SpanKind) -> int:
        """Convert SpanKind to protobuf."""
        mapping = {
            SpanKind.UNSPECIFIED: observe_pb2.SPAN_KIND_UNSPECIFIED,
            SpanKind.INTERNAL: observe_pb2.SPAN_KIND_INTERNAL,
            SpanKind.SERVER: observe_pb2.SPAN_KIND_SERVER,
            SpanKind.CLIENT: observe_pb2.SPAN_KIND_CLIENT,
            SpanKind.PRODUCER: observe_pb2.SPAN_KIND_PRODUCER,
            SpanKind.CONSUMER: observe_pb2.SPAN_KIND_CONSUMER,
        }
        return mapping.get(kind, observe_pb2.SPAN_KIND_UNSPECIFIED)

    def _kind_from_pb(self, kind: int) -> SpanKind:
        """Convert protobuf to SpanKind."""
        mapping = {
            observe_pb2.SPAN_KIND_UNSPECIFIED: SpanKind.UNSPECIFIED,
            observe_pb2.SPAN_KIND_INTERNAL: SpanKind.INTERNAL,
            observe_pb2.SPAN_KIND_SERVER: SpanKind.SERVER,
            observe_pb2.SPAN_KIND_CLIENT: SpanKind.CLIENT,
            observe_pb2.SPAN_KIND_PRODUCER: SpanKind.PRODUCER,
            observe_pb2.SPAN_KIND_CONSUMER: SpanKind.CONSUMER,
        }
        return mapping.get(kind, SpanKind.UNSPECIFIED)

    def _status_to_pb(self, status: SpanStatus) -> int:
        """Convert SpanStatus to protobuf."""
        mapping = {
            SpanStatus.UNSET: observe_pb2.SPAN_STATUS_UNSET,
            SpanStatus.OK: observe_pb2.SPAN_STATUS_OK,
            SpanStatus.ERROR: observe_pb2.SPAN_STATUS_ERROR,
        }
        return mapping.get(status, observe_pb2.SPAN_STATUS_UNSET)

    def _status_from_pb(self, status: int) -> SpanStatus:
        """Convert protobuf to SpanStatus."""
        mapping = {
            observe_pb2.SPAN_STATUS_UNSET: SpanStatus.UNSET,
            observe_pb2.SPAN_STATUS_OK: SpanStatus.OK,
            observe_pb2.SPAN_STATUS_ERROR: SpanStatus.ERROR,
        }
        return mapping.get(status, SpanStatus.UNSET)
