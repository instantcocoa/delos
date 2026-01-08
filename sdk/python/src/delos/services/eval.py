"""Client for the eval service."""

from __future__ import annotations

import sys
from datetime import timezone
from typing import TYPE_CHECKING

from delos.models.eval import (
    EvalConfig,
    EvalResult,
    EvalRun,
    EvalRunStatus,
    EvalSummary,
    Evaluator,
    EvaluatorConfig,
    EvaluatorParam,
    EvaluatorResult,
    ExampleComparison,
    RunComparison,
)
from delos.services.base import BaseClient

if TYPE_CHECKING:
    from delos.config import ServiceEndpoint

sys.path.insert(0, "gen/python")

try:
    from eval.v1 import eval_pb2, eval_pb2_grpc
except ImportError:
    eval_pb2 = None  # type: ignore
    eval_pb2_grpc = None  # type: ignore


class EvalClient(BaseClient):
    """Client for the eval service."""

    def __init__(self, endpoint: ServiceEndpoint, timeout: float = 30.0) -> None:
        """Initialize the eval client."""
        super().__init__(endpoint, timeout)
        self._stub: eval_pb2_grpc.EvalServiceStub | None = None

    @property
    def stub(self) -> eval_pb2_grpc.EvalServiceStub:
        """Get the gRPC stub."""
        if self._stub is None:
            if eval_pb2_grpc is None:
                raise ImportError("Generated protobuf code not found. Run 'buf generate' first.")
            self._stub = eval_pb2_grpc.EvalServiceStub(self.channel)
        return self._stub

    def create_run(
        self,
        name: str,
        *,
        description: str = "",
        prompt_id: str = "",
        prompt_version: int = 0,
        dataset_id: str = "",
        config: EvalConfig | None = None,
        metadata: dict[str, str] | None = None,
    ) -> EvalRun:
        """Create and start an evaluation run."""
        pb_config = None
        if config:
            pb_config = eval_pb2.EvalConfig(
                evaluators=[
                    eval_pb2.EvaluatorConfig(
                        type=e.type,
                        name=e.name,
                        params=e.params,
                        weight=e.weight,
                    )
                    for e in config.evaluators
                ],
                provider=config.provider,
                model=config.model,
                concurrency=config.concurrency,
                sample_size=config.sample_size,
                shuffle=config.shuffle,
            )

        request = eval_pb2.CreateEvalRunRequest(
            name=name,
            description=description,
            prompt_id=prompt_id,
            prompt_version=prompt_version,
            dataset_id=dataset_id,
            config=pb_config,
            metadata=metadata or {},
        )

        response = self.stub.CreateEvalRun(request, timeout=self._timeout)
        return self._to_run(response.eval_run)

    def get_run(self, id: str) -> EvalRun | None:
        """Get an evaluation run by ID."""
        request = eval_pb2.GetEvalRunRequest(id=id)
        try:
            response = self.stub.GetEvalRun(request, timeout=self._timeout)
            return self._to_run(response.eval_run)
        except Exception:
            return None

    def list_runs(
        self,
        *,
        prompt_id: str = "",
        dataset_id: str = "",
        status: EvalRunStatus = EvalRunStatus.UNSPECIFIED,
        limit: int = 100,
        offset: int = 0,
    ) -> tuple[list[EvalRun], int]:
        """List evaluation runs."""
        request = eval_pb2.ListEvalRunsRequest(
            prompt_id=prompt_id,
            dataset_id=dataset_id,
            status=self._status_to_pb(status),
            limit=limit,
            offset=offset,
        )
        response = self.stub.ListEvalRuns(request, timeout=self._timeout)
        runs = [self._to_run(r) for r in response.eval_runs]
        return runs, response.total_count

    def cancel_run(self, id: str) -> EvalRun:
        """Cancel a running evaluation."""
        request = eval_pb2.CancelEvalRunRequest(id=id)
        response = self.stub.CancelEvalRun(request, timeout=self._timeout)
        return self._to_run(response.eval_run)

    def get_results(
        self,
        eval_run_id: str,
        *,
        failed_only: bool = False,
        limit: int = 100,
        offset: int = 0,
    ) -> tuple[list[EvalResult], int]:
        """Get results for an evaluation run."""
        request = eval_pb2.GetEvalResultsRequest(
            eval_run_id=eval_run_id,
            failed_only=failed_only,
            limit=limit,
            offset=offset,
        )
        response = self.stub.GetEvalResults(request, timeout=self._timeout)
        results = [self._to_result(r) for r in response.results]
        return results, response.total_count

    def compare_runs(
        self,
        run_id_a: str,
        run_id_b: str,
    ) -> tuple[RunComparison, RunComparison, list[ExampleComparison]]:
        """Compare two evaluation runs."""
        request = eval_pb2.CompareRunsRequest(
            run_id_a=run_id_a,
            run_id_b=run_id_b,
        )
        response = self.stub.CompareRuns(request, timeout=self._timeout)

        run_a = RunComparison(
            run_id=response.run_a.run_id,
            prompt_version=response.run_a.prompt_version,
            overall_score=response.run_a.overall_score,
            pass_rate=response.run_a.pass_rate,
            avg_latency_ms=response.run_a.avg_latency_ms,
            total_cost_usd=response.run_a.total_cost_usd,
        )
        run_b = RunComparison(
            run_id=response.run_b.run_id,
            prompt_version=response.run_b.prompt_version,
            overall_score=response.run_b.overall_score,
            pass_rate=response.run_b.pass_rate,
            avg_latency_ms=response.run_b.avg_latency_ms,
            total_cost_usd=response.run_b.total_cost_usd,
        )
        examples = [
            ExampleComparison(
                example_id=e.example_id,
                score_a=e.score_a,
                score_b=e.score_b,
                score_diff=e.score_diff,
                regression=e.regression,
            )
            for e in response.examples
        ]
        return run_a, run_b, examples

    def list_evaluators(self) -> list[Evaluator]:
        """List available evaluator types."""
        request = eval_pb2.ListEvaluatorsRequest()
        response = self.stub.ListEvaluators(request, timeout=self._timeout)
        return [
            Evaluator(
                type=e.type,
                name=e.name,
                description=e.description,
                params=[
                    EvaluatorParam(
                        name=p.name,
                        type=p.type,
                        description=p.description,
                        required=p.required,
                        default_value=p.default_value,
                    )
                    for p in e.params
                ],
            )
            for e in response.evaluators
        ]

    def _to_run(self, pb: eval_pb2.EvalRun) -> EvalRun:
        """Convert protobuf to model."""
        config = None
        if pb.HasField("config"):
            config = EvalConfig(
                evaluators=[
                    EvaluatorConfig(
                        type=e.type,
                        name=e.name,
                        params=dict(e.params),
                        weight=e.weight,
                    )
                    for e in pb.config.evaluators
                ],
                provider=pb.config.provider,
                model=pb.config.model,
                concurrency=pb.config.concurrency,
                sample_size=pb.config.sample_size,
                shuffle=pb.config.shuffle,
            )

        summary = None
        if pb.HasField("summary"):
            summary = EvalSummary(
                overall_score=pb.summary.overall_score,
                scores_by_evaluator=dict(pb.summary.scores_by_evaluator),
                passed_count=pb.summary.passed_count,
                failed_count=pb.summary.failed_count,
                pass_rate=pb.summary.pass_rate,
                total_cost_usd=pb.summary.total_cost_usd,
                total_tokens=pb.summary.total_tokens,
                avg_latency_ms=pb.summary.avg_latency_ms,
            )

        return EvalRun(
            id=pb.id,
            name=pb.name,
            description=pb.description,
            prompt_id=pb.prompt_id,
            prompt_version=pb.prompt_version,
            dataset_id=pb.dataset_id,
            config=config,
            status=self._status_from_pb(pb.status),
            error_message=pb.error_message,
            total_examples=pb.total_examples,
            completed_examples=pb.completed_examples,
            summary=summary,
            created_at=pb.created_at.ToDatetime(timezone.utc) if pb.HasField("created_at") else None,
            started_at=pb.started_at.ToDatetime(timezone.utc) if pb.HasField("started_at") else None,
            completed_at=pb.completed_at.ToDatetime(timezone.utc) if pb.HasField("completed_at") else None,
            created_by=pb.created_by,
            metadata=dict(pb.metadata),
        )

    def _to_result(self, pb: eval_pb2.EvalResult) -> EvalResult:
        """Convert protobuf result to model."""
        from google.protobuf.json_format import MessageToDict

        evaluator_results = {}
        for k, v in pb.evaluator_results.items():
            evaluator_results[k] = EvaluatorResult(
                evaluator_type=v.evaluator_type,
                score=v.score,
                passed=v.passed,
                explanation=v.explanation,
                details=dict(v.details),
            )

        return EvalResult(
            id=pb.id,
            eval_run_id=pb.eval_run_id,
            example_id=pb.example_id,
            input=MessageToDict(pb.input) if pb.HasField("input") else {},
            expected_output=MessageToDict(pb.expected_output) if pb.HasField("expected_output") else {},
            actual_output=MessageToDict(pb.actual_output) if pb.HasField("actual_output") else {},
            evaluator_results=evaluator_results,
            overall_score=pb.overall_score,
            passed=pb.passed,
            latency_ms=pb.latency_ms,
            tokens_used=pb.tokens_used,
            cost_usd=pb.cost_usd,
            error=pb.error,
        )

    def _status_to_pb(self, status: EvalRunStatus) -> int:
        """Convert status to protobuf."""
        mapping = {
            EvalRunStatus.UNSPECIFIED: eval_pb2.EVAL_RUN_STATUS_UNSPECIFIED,
            EvalRunStatus.PENDING: eval_pb2.EVAL_RUN_STATUS_PENDING,
            EvalRunStatus.RUNNING: eval_pb2.EVAL_RUN_STATUS_RUNNING,
            EvalRunStatus.COMPLETED: eval_pb2.EVAL_RUN_STATUS_COMPLETED,
            EvalRunStatus.FAILED: eval_pb2.EVAL_RUN_STATUS_FAILED,
            EvalRunStatus.CANCELLED: eval_pb2.EVAL_RUN_STATUS_CANCELLED,
        }
        return mapping.get(status, eval_pb2.EVAL_RUN_STATUS_UNSPECIFIED)

    def _status_from_pb(self, status: int) -> EvalRunStatus:
        """Convert protobuf to status."""
        mapping = {
            eval_pb2.EVAL_RUN_STATUS_UNSPECIFIED: EvalRunStatus.UNSPECIFIED,
            eval_pb2.EVAL_RUN_STATUS_PENDING: EvalRunStatus.PENDING,
            eval_pb2.EVAL_RUN_STATUS_RUNNING: EvalRunStatus.RUNNING,
            eval_pb2.EVAL_RUN_STATUS_COMPLETED: EvalRunStatus.COMPLETED,
            eval_pb2.EVAL_RUN_STATUS_FAILED: EvalRunStatus.FAILED,
            eval_pb2.EVAL_RUN_STATUS_CANCELLED: EvalRunStatus.CANCELLED,
        }
        return mapping.get(status, EvalRunStatus.UNSPECIFIED)
