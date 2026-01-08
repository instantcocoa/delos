"""Pydantic models for the Delos SDK."""

from delos.models.common import Metadata, PaginatedResponse
from delos.models.datasets import (
    Dataset,
    DatasetSchema,
    Example,
    ExampleInput,
    ExampleSource,
    SchemaField,
)
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
from delos.models.observe import Span, SpanKind, SpanStatus, Trace
from delos.models.prompt import Prompt, PromptMessage, PromptVariable, PromptVersion
from delos.models.runtime import (
    CompletionParams,
    CompletionResponse,
    Message,
    Model,
    Provider,
    RoutingStrategy,
    Usage,
)

__all__ = [
    # Common
    "Metadata",
    "PaginatedResponse",
    # Observe
    "Span",
    "SpanKind",
    "SpanStatus",
    "Trace",
    # Runtime
    "CompletionParams",
    "CompletionResponse",
    "Message",
    "Model",
    "Provider",
    "RoutingStrategy",
    "Usage",
    # Prompt
    "Prompt",
    "PromptMessage",
    "PromptVariable",
    "PromptVersion",
    # Datasets
    "Dataset",
    "DatasetSchema",
    "Example",
    "ExampleInput",
    "ExampleSource",
    "SchemaField",
    # Eval
    "EvalConfig",
    "EvalResult",
    "EvalRun",
    "EvalRunStatus",
    "EvalSummary",
    "Evaluator",
    "EvaluatorConfig",
    "EvaluatorParam",
    "EvaluatorResult",
    "ExampleComparison",
    "RunComparison",
    # Deploy
    "ConditionResult",
    "Deployment",
    "DeploymentMetrics",
    "DeploymentStatus",
    "DeploymentStrategy",
    "DeploymentType",
    "GateCondition",
    "QualityGate",
    "QualityGateResult",
    "RolloutProgress",
]
