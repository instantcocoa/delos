"""Models for the deploy service."""

from __future__ import annotations

from datetime import datetime
from enum import Enum

from pydantic import BaseModel


class DeploymentStatus(str, Enum):
    """Status of a deployment."""

    UNSPECIFIED = "unspecified"
    PENDING_APPROVAL = "pending_approval"
    PENDING_GATES = "pending_gates"
    GATES_FAILED = "gates_failed"
    IN_PROGRESS = "in_progress"
    COMPLETED = "completed"
    ROLLED_BACK = "rolled_back"
    CANCELLED = "cancelled"
    FAILED = "failed"


class DeploymentType(str, Enum):
    """Type of deployment strategy."""

    UNSPECIFIED = "unspecified"
    IMMEDIATE = "immediate"
    GRADUAL = "gradual"
    CANARY = "canary"
    BLUE_GREEN = "blue_green"


class DeploymentStrategy(BaseModel):
    """Strategy for deploying a new version."""

    type: DeploymentType = DeploymentType.IMMEDIATE
    initial_percentage: int = 0
    increment: int = 0
    interval_seconds: int = 0
    auto_rollback: bool = False
    rollback_threshold: float = 0.0


class RolloutProgress(BaseModel):
    """Progress of a gradual rollout."""

    current_percentage: int = 0
    target_percentage: int = 100
    last_increment_at: datetime | None = None
    next_increment_at: datetime | None = None


class GateCondition(BaseModel):
    """A condition in a quality gate."""

    type: str  # eval_score, latency, cost, custom
    operator: str  # gte, lte, eq
    threshold: float
    eval_run_id: str = ""
    dataset_id: str = ""


class ConditionResult(BaseModel):
    """Result of evaluating a condition."""

    type: str
    expected: float
    actual: float
    passed: bool


class QualityGateResult(BaseModel):
    """Result of evaluating a quality gate."""

    gate_id: str
    gate_name: str
    passed: bool
    message: str = ""
    condition_results: list[ConditionResult] = []


class QualityGate(BaseModel):
    """A quality gate configuration."""

    id: str
    name: str
    prompt_id: str
    conditions: list[GateCondition] = []
    required: bool = True
    created_at: datetime | None = None
    created_by: str = ""


class DeploymentMetrics(BaseModel):
    """Real-time metrics for a deployment."""

    avg_latency_ms: float = 0.0
    error_rate: float = 0.0
    quality_score: float = 0.0
    request_count: int = 0


class Deployment(BaseModel):
    """A deployment of a prompt version."""

    id: str
    prompt_id: str
    from_version: int = 0
    to_version: int = 0
    environment: str = ""
    strategy: DeploymentStrategy | None = None
    status: DeploymentStatus = DeploymentStatus.UNSPECIFIED
    status_message: str = ""
    gate_results: list[QualityGateResult] = []
    gates_passed: bool = False
    rollout: RolloutProgress | None = None
    created_at: datetime | None = None
    started_at: datetime | None = None
    completed_at: datetime | None = None
    created_by: str = ""
    approved_by: str = ""
    metadata: dict[str, str] = {}

    @property
    def is_active(self) -> bool:
        """Check if deployment is currently active."""
        return self.status in (
            DeploymentStatus.PENDING_APPROVAL,
            DeploymentStatus.PENDING_GATES,
            DeploymentStatus.IN_PROGRESS,
        )

    @property
    def is_complete(self) -> bool:
        """Check if deployment has completed (successfully or not)."""
        return self.status in (
            DeploymentStatus.COMPLETED,
            DeploymentStatus.ROLLED_BACK,
            DeploymentStatus.CANCELLED,
            DeploymentStatus.FAILED,
            DeploymentStatus.GATES_FAILED,
        )
