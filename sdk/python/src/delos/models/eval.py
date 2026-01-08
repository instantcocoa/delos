"""Models for the eval service."""

from __future__ import annotations

from datetime import datetime
from enum import Enum
from typing import Any

from pydantic import BaseModel


class EvalRunStatus(str, Enum):
    """Status of an evaluation run."""

    UNSPECIFIED = "unspecified"
    PENDING = "pending"
    RUNNING = "running"
    COMPLETED = "completed"
    FAILED = "failed"
    CANCELLED = "cancelled"


class EvaluatorConfig(BaseModel):
    """Configuration for a single evaluator."""

    type: str  # exact_match, semantic_similarity, llm_judge, etc.
    name: str = ""
    params: dict[str, str] = {}
    weight: float = 1.0


class EvalConfig(BaseModel):
    """Configuration for an evaluation run."""

    evaluators: list[EvaluatorConfig] = []
    provider: str = ""
    model: str = ""
    concurrency: int = 1
    sample_size: int = 0  # 0 = all examples
    shuffle: bool = False


class EvaluatorResult(BaseModel):
    """Result from a single evaluator."""

    evaluator_type: str
    score: float  # 0-1
    passed: bool
    explanation: str = ""
    details: dict[str, str] = {}


class EvalResult(BaseModel):
    """Result for a single example."""

    id: str
    eval_run_id: str
    example_id: str
    input: dict[str, Any] = {}
    expected_output: dict[str, Any] = {}
    actual_output: dict[str, Any] = {}
    evaluator_results: dict[str, EvaluatorResult] = {}
    overall_score: float = 0.0
    passed: bool = False
    latency_ms: float = 0.0
    tokens_used: int = 0
    cost_usd: float = 0.0
    error: str = ""


class EvalSummary(BaseModel):
    """Summary statistics for an evaluation run."""

    overall_score: float = 0.0
    scores_by_evaluator: dict[str, float] = {}
    passed_count: int = 0
    failed_count: int = 0
    pass_rate: float = 0.0
    total_cost_usd: float = 0.0
    total_tokens: int = 0
    avg_latency_ms: float = 0.0


class EvalRun(BaseModel):
    """An evaluation run."""

    id: str
    name: str
    description: str = ""
    prompt_id: str = ""
    prompt_version: int = 0
    dataset_id: str = ""
    config: EvalConfig | None = None
    status: EvalRunStatus = EvalRunStatus.UNSPECIFIED
    error_message: str = ""
    total_examples: int = 0
    completed_examples: int = 0
    summary: EvalSummary | None = None
    created_at: datetime | None = None
    started_at: datetime | None = None
    completed_at: datetime | None = None
    created_by: str = ""
    metadata: dict[str, str] = {}

    @property
    def progress(self) -> float:
        """Get completion progress as a percentage."""
        if self.total_examples == 0:
            return 0.0
        return (self.completed_examples / self.total_examples) * 100


class EvaluatorParam(BaseModel):
    """Parameter definition for an evaluator."""

    name: str
    type: str
    description: str = ""
    required: bool = False
    default_value: str = ""


class Evaluator(BaseModel):
    """An available evaluator type."""

    type: str
    name: str
    description: str = ""
    params: list[EvaluatorParam] = []


class RunComparison(BaseModel):
    """Summary for comparing runs."""

    run_id: str
    prompt_version: str = ""
    overall_score: float = 0.0
    pass_rate: float = 0.0
    avg_latency_ms: float = 0.0
    total_cost_usd: float = 0.0


class ExampleComparison(BaseModel):
    """Comparison of a single example across runs."""

    example_id: str
    score_a: float = 0.0
    score_b: float = 0.0
    score_diff: float = 0.0
    regression: bool = False
