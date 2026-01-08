"""Models for the datasets service."""

from __future__ import annotations

from datetime import datetime
from enum import Enum
from typing import Any

from pydantic import BaseModel


class ExampleSource(str, Enum):
    """Source of an example."""

    UNSPECIFIED = "unspecified"
    MANUAL = "manual"
    GENERATED = "generated"
    PRODUCTION = "production"
    IMPORTED = "imported"


class SchemaField(BaseModel):
    """A field in the dataset schema."""

    name: str
    type: str  # string, number, boolean, json, array
    description: str = ""
    required: bool = True


class DatasetSchema(BaseModel):
    """Schema defining the structure of examples."""

    input_fields: list[SchemaField] = []
    expected_output_fields: list[SchemaField] = []


class ExampleInput(BaseModel):
    """Input for creating an example."""

    input: dict[str, Any] = {}
    expected_output: dict[str, Any] = {}
    metadata: dict[str, str] = {}
    source: ExampleSource = ExampleSource.MANUAL


class Example(BaseModel):
    """A single example in a dataset."""

    id: str
    dataset_id: str
    input: dict[str, Any] = {}
    expected_output: dict[str, Any] = {}
    metadata: dict[str, str] = {}
    source: ExampleSource = ExampleSource.UNSPECIFIED
    created_at: datetime | None = None


class Dataset(BaseModel):
    """A dataset containing examples for evaluation."""

    id: str
    name: str
    description: str = ""
    prompt_id: str = ""
    schema_: DatasetSchema | None = None
    example_count: int = 0
    last_updated: datetime | None = None
    tags: list[str] = []
    metadata: dict[str, str] = {}
    version: int = 1
    created_by: str = ""
    created_at: datetime | None = None

    class Config:
        """Pydantic config."""

        # Allow schema_ to map from 'schema' in JSON
        populate_by_name = True

    def __init__(self, **data: Any) -> None:
        """Handle 'schema' field name mapping."""
        if "schema" in data and "schema_" not in data:
            data["schema_"] = data.pop("schema")
        super().__init__(**data)
