"""Client for the deploy service."""

from __future__ import annotations

import sys
from datetime import timezone
from typing import TYPE_CHECKING

from delos.models.deploy import (
    ConditionResult,
    Deployment,
    DeploymentMetrics,
    DeploymentStatus,
    DeploymentStrategy,
    DeploymentType,
    GateCondition,
    QualityGate,
    QualityGateResult,
    RolloutProgress,
)
from delos.services.base import BaseClient

if TYPE_CHECKING:
    from delos.config import ServiceEndpoint

sys.path.insert(0, "gen/python")

try:
    from deploy.v1 import deploy_pb2, deploy_pb2_grpc
except ImportError:
    deploy_pb2 = None  # type: ignore
    deploy_pb2_grpc = None  # type: ignore


class DeployClient(BaseClient):
    """Client for the deploy service."""

    def __init__(self, endpoint: ServiceEndpoint, timeout: float = 30.0) -> None:
        """Initialize the deploy client."""
        super().__init__(endpoint, timeout)
        self._stub: deploy_pb2_grpc.DeployServiceStub | None = None

    @property
    def stub(self) -> deploy_pb2_grpc.DeployServiceStub:
        """Get the gRPC stub."""
        if self._stub is None:
            if deploy_pb2_grpc is None:
                raise ImportError("Generated protobuf code not found. Run 'buf generate' first.")
            self._stub = deploy_pb2_grpc.DeployServiceStub(self.channel)
        return self._stub

    def create(
        self,
        prompt_id: str,
        to_version: int,
        *,
        environment: str = "production",
        strategy: DeploymentStrategy | None = None,
        skip_approval: bool = False,
        metadata: dict[str, str] | None = None,
    ) -> Deployment:
        """Create a new deployment."""
        pb_strategy = None
        if strategy:
            pb_strategy = deploy_pb2.DeploymentStrategy(
                type=self._type_to_pb(strategy.type),
                initial_percentage=strategy.initial_percentage,
                increment=strategy.increment,
                interval_seconds=strategy.interval_seconds,
                auto_rollback=strategy.auto_rollback,
                rollback_threshold=strategy.rollback_threshold,
            )

        request = deploy_pb2.CreateDeploymentRequest(
            prompt_id=prompt_id,
            to_version=to_version,
            environment=environment,
            strategy=pb_strategy,
            skip_approval=skip_approval,
            metadata=metadata or {},
        )

        response = self.stub.CreateDeployment(request, timeout=self._timeout)
        return self._to_deployment(response.deployment)

    def get(self, id: str) -> Deployment | None:
        """Get a deployment by ID."""
        request = deploy_pb2.GetDeploymentRequest(id=id)
        try:
            response = self.stub.GetDeployment(request, timeout=self._timeout)
            return self._to_deployment(response.deployment)
        except Exception:
            return None

    def list(
        self,
        *,
        prompt_id: str = "",
        environment: str = "",
        status: DeploymentStatus = DeploymentStatus.UNSPECIFIED,
        limit: int = 100,
        offset: int = 0,
    ) -> tuple[list[Deployment], int]:
        """List deployments."""
        request = deploy_pb2.ListDeploymentsRequest(
            prompt_id=prompt_id,
            environment=environment,
            status=self._status_to_pb(status),
            limit=limit,
            offset=offset,
        )
        response = self.stub.ListDeployments(request, timeout=self._timeout)
        deployments = [self._to_deployment(d) for d in response.deployments]
        return deployments, response.total_count

    def approve(self, id: str, *, comment: str = "") -> Deployment:
        """Approve a pending deployment."""
        request = deploy_pb2.ApproveDeploymentRequest(id=id, comment=comment)
        response = self.stub.ApproveDeployment(request, timeout=self._timeout)
        return self._to_deployment(response.deployment)

    def rollback(self, id: str, *, reason: str = "") -> tuple[Deployment, Deployment]:
        """Rollback a deployment."""
        request = deploy_pb2.RollbackDeploymentRequest(id=id, reason=reason)
        response = self.stub.RollbackDeployment(request, timeout=self._timeout)
        return (
            self._to_deployment(response.deployment),
            self._to_deployment(response.rollback_deployment),
        )

    def cancel(self, id: str, *, reason: str = "") -> Deployment:
        """Cancel a pending/in-progress deployment."""
        request = deploy_pb2.CancelDeploymentRequest(id=id, reason=reason)
        response = self.stub.CancelDeployment(request, timeout=self._timeout)
        return self._to_deployment(response.deployment)

    def get_status(
        self, id: str
    ) -> tuple[DeploymentStatus, RolloutProgress | None, list[QualityGateResult]]:
        """Get real-time deployment status."""
        request = deploy_pb2.GetDeploymentStatusRequest(id=id)
        response = self.stub.GetDeploymentStatus(request, timeout=self._timeout)

        rollout = None
        if response.HasField("rollout"):
            rollout = RolloutProgress(
                current_percentage=response.rollout.current_percentage,
                target_percentage=response.rollout.target_percentage,
                last_increment_at=response.rollout.last_increment_at.ToDatetime(timezone.utc)
                if response.rollout.HasField("last_increment_at")
                else None,
                next_increment_at=response.rollout.next_increment_at.ToDatetime(timezone.utc)
                if response.rollout.HasField("next_increment_at")
                else None,
            )

        gate_results = [self._to_gate_result(r) for r in response.gate_results]
        return self._status_from_pb(response.status), rollout, gate_results

    def create_quality_gate(
        self,
        name: str,
        prompt_id: str,
        *,
        conditions: list[GateCondition] | None = None,
        required: bool = True,
    ) -> QualityGate:
        """Create a quality gate."""
        pb_conditions = []
        if conditions:
            for c in conditions:
                pb_conditions.append(
                    deploy_pb2.GateCondition(
                        type=c.type,
                        operator=c.operator,
                        threshold=c.threshold,
                        eval_run_id=c.eval_run_id,
                        dataset_id=c.dataset_id,
                    )
                )

        request = deploy_pb2.CreateQualityGateRequest(
            name=name,
            prompt_id=prompt_id,
            conditions=pb_conditions,
            required=required,
        )
        response = self.stub.CreateQualityGate(request, timeout=self._timeout)
        return self._to_quality_gate(response.quality_gate)

    def list_quality_gates(self, prompt_id: str) -> list[QualityGate]:
        """List quality gates for a prompt."""
        request = deploy_pb2.ListQualityGatesRequest(prompt_id=prompt_id)
        response = self.stub.ListQualityGates(request, timeout=self._timeout)
        return [self._to_quality_gate(g) for g in response.quality_gates]

    def _to_deployment(self, pb: deploy_pb2.Deployment) -> Deployment:
        """Convert protobuf to model."""
        strategy = None
        if pb.HasField("strategy"):
            strategy = DeploymentStrategy(
                type=self._type_from_pb(pb.strategy.type),
                initial_percentage=pb.strategy.initial_percentage,
                increment=pb.strategy.increment,
                interval_seconds=pb.strategy.interval_seconds,
                auto_rollback=pb.strategy.auto_rollback,
                rollback_threshold=pb.strategy.rollback_threshold,
            )

        rollout = None
        if pb.HasField("rollout"):
            rollout = RolloutProgress(
                current_percentage=pb.rollout.current_percentage,
                target_percentage=pb.rollout.target_percentage,
                last_increment_at=pb.rollout.last_increment_at.ToDatetime(timezone.utc)
                if pb.rollout.HasField("last_increment_at")
                else None,
                next_increment_at=pb.rollout.next_increment_at.ToDatetime(timezone.utc)
                if pb.rollout.HasField("next_increment_at")
                else None,
            )

        return Deployment(
            id=pb.id,
            prompt_id=pb.prompt_id,
            from_version=pb.from_version,
            to_version=pb.to_version,
            environment=pb.environment,
            strategy=strategy,
            status=self._status_from_pb(pb.status),
            status_message=pb.status_message,
            gate_results=[self._to_gate_result(r) for r in pb.gate_results],
            gates_passed=pb.gates_passed,
            rollout=rollout,
            created_at=pb.created_at.ToDatetime(timezone.utc) if pb.HasField("created_at") else None,
            started_at=pb.started_at.ToDatetime(timezone.utc) if pb.HasField("started_at") else None,
            completed_at=pb.completed_at.ToDatetime(timezone.utc) if pb.HasField("completed_at") else None,
            created_by=pb.created_by,
            approved_by=pb.approved_by,
            metadata=dict(pb.metadata),
        )

    def _to_gate_result(self, pb: deploy_pb2.QualityGateResult) -> QualityGateResult:
        """Convert protobuf gate result to model."""
        return QualityGateResult(
            gate_id=pb.gate_id,
            gate_name=pb.gate_name,
            passed=pb.passed,
            message=pb.message,
            condition_results=[
                ConditionResult(
                    type=c.type,
                    expected=c.expected,
                    actual=c.actual,
                    passed=c.passed,
                )
                for c in pb.condition_results
            ],
        )

    def _to_quality_gate(self, pb: deploy_pb2.QualityGate) -> QualityGate:
        """Convert protobuf quality gate to model."""
        return QualityGate(
            id=pb.id,
            name=pb.name,
            prompt_id=pb.prompt_id,
            conditions=[
                GateCondition(
                    type=c.type,
                    operator=c.operator,
                    threshold=c.threshold,
                    eval_run_id=c.eval_run_id,
                    dataset_id=c.dataset_id,
                )
                for c in pb.conditions
            ],
            required=pb.required,
            created_at=pb.created_at.ToDatetime(timezone.utc) if pb.HasField("created_at") else None,
            created_by=pb.created_by,
        )

    def _type_to_pb(self, t: DeploymentType) -> int:
        """Convert DeploymentType to protobuf."""
        mapping = {
            DeploymentType.UNSPECIFIED: deploy_pb2.DEPLOYMENT_TYPE_UNSPECIFIED,
            DeploymentType.IMMEDIATE: deploy_pb2.DEPLOYMENT_TYPE_IMMEDIATE,
            DeploymentType.GRADUAL: deploy_pb2.DEPLOYMENT_TYPE_GRADUAL,
            DeploymentType.CANARY: deploy_pb2.DEPLOYMENT_TYPE_CANARY,
            DeploymentType.BLUE_GREEN: deploy_pb2.DEPLOYMENT_TYPE_BLUE_GREEN,
        }
        return mapping.get(t, deploy_pb2.DEPLOYMENT_TYPE_UNSPECIFIED)

    def _type_from_pb(self, t: int) -> DeploymentType:
        """Convert protobuf to DeploymentType."""
        mapping = {
            deploy_pb2.DEPLOYMENT_TYPE_UNSPECIFIED: DeploymentType.UNSPECIFIED,
            deploy_pb2.DEPLOYMENT_TYPE_IMMEDIATE: DeploymentType.IMMEDIATE,
            deploy_pb2.DEPLOYMENT_TYPE_GRADUAL: DeploymentType.GRADUAL,
            deploy_pb2.DEPLOYMENT_TYPE_CANARY: DeploymentType.CANARY,
            deploy_pb2.DEPLOYMENT_TYPE_BLUE_GREEN: DeploymentType.BLUE_GREEN,
        }
        return mapping.get(t, DeploymentType.UNSPECIFIED)

    def _status_to_pb(self, s: DeploymentStatus) -> int:
        """Convert DeploymentStatus to protobuf."""
        mapping = {
            DeploymentStatus.UNSPECIFIED: deploy_pb2.DEPLOYMENT_STATUS_UNSPECIFIED,
            DeploymentStatus.PENDING_APPROVAL: deploy_pb2.DEPLOYMENT_STATUS_PENDING_APPROVAL,
            DeploymentStatus.PENDING_GATES: deploy_pb2.DEPLOYMENT_STATUS_PENDING_GATES,
            DeploymentStatus.GATES_FAILED: deploy_pb2.DEPLOYMENT_STATUS_GATES_FAILED,
            DeploymentStatus.IN_PROGRESS: deploy_pb2.DEPLOYMENT_STATUS_IN_PROGRESS,
            DeploymentStatus.COMPLETED: deploy_pb2.DEPLOYMENT_STATUS_COMPLETED,
            DeploymentStatus.ROLLED_BACK: deploy_pb2.DEPLOYMENT_STATUS_ROLLED_BACK,
            DeploymentStatus.CANCELLED: deploy_pb2.DEPLOYMENT_STATUS_CANCELLED,
            DeploymentStatus.FAILED: deploy_pb2.DEPLOYMENT_STATUS_FAILED,
        }
        return mapping.get(s, deploy_pb2.DEPLOYMENT_STATUS_UNSPECIFIED)

    def _status_from_pb(self, s: int) -> DeploymentStatus:
        """Convert protobuf to DeploymentStatus."""
        mapping = {
            deploy_pb2.DEPLOYMENT_STATUS_UNSPECIFIED: DeploymentStatus.UNSPECIFIED,
            deploy_pb2.DEPLOYMENT_STATUS_PENDING_APPROVAL: DeploymentStatus.PENDING_APPROVAL,
            deploy_pb2.DEPLOYMENT_STATUS_PENDING_GATES: DeploymentStatus.PENDING_GATES,
            deploy_pb2.DEPLOYMENT_STATUS_GATES_FAILED: DeploymentStatus.GATES_FAILED,
            deploy_pb2.DEPLOYMENT_STATUS_IN_PROGRESS: DeploymentStatus.IN_PROGRESS,
            deploy_pb2.DEPLOYMENT_STATUS_COMPLETED: DeploymentStatus.COMPLETED,
            deploy_pb2.DEPLOYMENT_STATUS_ROLLED_BACK: DeploymentStatus.ROLLED_BACK,
            deploy_pb2.DEPLOYMENT_STATUS_CANCELLED: DeploymentStatus.CANCELLED,
            deploy_pb2.DEPLOYMENT_STATUS_FAILED: DeploymentStatus.FAILED,
        }
        return mapping.get(s, DeploymentStatus.UNSPECIFIED)
