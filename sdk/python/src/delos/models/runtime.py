"""Models for the runtime service."""

from __future__ import annotations

from enum import Enum

from pydantic import BaseModel, Field


class RoutingStrategy(str, Enum):
    """Strategy for routing requests to providers."""

    UNSPECIFIED = "unspecified"
    COST = "cost"
    LATENCY = "latency"
    QUALITY = "quality"


class Message(BaseModel):
    """A message in a conversation."""

    role: str  # system, user, assistant
    content: str


class CompletionParams(BaseModel):
    """Parameters for a completion request."""

    model: str = ""
    messages: list[Message] = []
    system_prompt: str = ""
    max_tokens: int = 1024
    temperature: float = 0.7
    top_p: float = 1.0
    stop_sequences: list[str] = []
    provider: str = ""  # optional: openai, anthropic
    routing_strategy: RoutingStrategy = RoutingStrategy.UNSPECIFIED
    metadata: dict[str, str] = {}


class Usage(BaseModel):
    """Token usage information."""

    prompt_tokens: int = 0
    completion_tokens: int = 0
    total_tokens: int = 0


class CompletionResponse(BaseModel):
    """Response from a completion request."""

    id: str = ""
    content: str = ""
    model: str = ""
    provider: str = ""
    usage: Usage = Field(default_factory=Usage)
    latency_ms: float = 0.0
    finish_reason: str = ""
    metadata: dict[str, str] = {}


class Model(BaseModel):
    """Information about an available model."""

    id: str
    name: str
    provider: str
    context_window: int = 0
    max_output_tokens: int = 0
    supports_vision: bool = False
    supports_function_calling: bool = False
    cost_per_input_token: float = 0.0
    cost_per_output_token: float = 0.0


class Provider(BaseModel):
    """Information about an LLM provider."""

    id: str
    name: str
    models: list[Model] = []
    is_available: bool = True
