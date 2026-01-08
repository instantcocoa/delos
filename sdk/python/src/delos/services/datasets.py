"""Client for the datasets service."""

from __future__ import annotations

import sys
from datetime import timezone
from typing import TYPE_CHECKING, Any

from google.protobuf import struct_pb2

from delos.models.datasets import (
    Dataset,
    DatasetSchema,
    Example,
    ExampleInput,
    ExampleSource,
    SchemaField,
)
from delos.services.base import BaseClient

if TYPE_CHECKING:
    from delos.config import ServiceEndpoint

sys.path.insert(0, "gen/python")

try:
    from datasets.v1 import datasets_pb2, datasets_pb2_grpc
except ImportError:
    datasets_pb2 = None  # type: ignore
    datasets_pb2_grpc = None  # type: ignore


class DatasetsClient(BaseClient):
    """Client for the datasets service."""

    def __init__(self, endpoint: ServiceEndpoint, timeout: float = 30.0) -> None:
        """Initialize the datasets client."""
        super().__init__(endpoint, timeout)
        self._stub: datasets_pb2_grpc.DatasetsServiceStub | None = None

    @property
    def stub(self) -> datasets_pb2_grpc.DatasetsServiceStub:
        """Get the gRPC stub."""
        if self._stub is None:
            if datasets_pb2_grpc is None:
                raise ImportError("Generated protobuf code not found. Run 'buf generate' first.")
            self._stub = datasets_pb2_grpc.DatasetsServiceStub(self.channel)
        return self._stub

    def create(
        self,
        name: str,
        *,
        description: str = "",
        prompt_id: str = "",
        schema: DatasetSchema | None = None,
        tags: list[str] | None = None,
        metadata: dict[str, str] | None = None,
    ) -> Dataset:
        """Create a new dataset.

        Args:
            name: Dataset name.
            description: Dataset description.
            prompt_id: ID of linked prompt.
            schema: Schema for examples.
            tags: Tags for categorization.
            metadata: Additional metadata.

        Returns:
            The created dataset.
        """
        pb_schema = None
        if schema:
            pb_schema = datasets_pb2.DatasetSchema(
                input_fields=[
                    datasets_pb2.SchemaField(
                        name=f.name,
                        type=f.type,
                        description=f.description,
                        required=f.required,
                    )
                    for f in schema.input_fields
                ],
                expected_output_fields=[
                    datasets_pb2.SchemaField(
                        name=f.name,
                        type=f.type,
                        description=f.description,
                        required=f.required,
                    )
                    for f in schema.expected_output_fields
                ],
            )

        request = datasets_pb2.CreateDatasetRequest(
            name=name,
            description=description,
            prompt_id=prompt_id,
            schema=pb_schema,
            tags=tags or [],
            metadata=metadata or {},
        )

        response = self.stub.CreateDataset(request, timeout=self._timeout)
        return self._to_dataset(response.dataset)

    def get(self, id: str) -> Dataset | None:
        """Get a dataset by ID."""
        request = datasets_pb2.GetDatasetRequest(id=id)
        try:
            response = self.stub.GetDataset(request, timeout=self._timeout)
            return self._to_dataset(response.dataset)
        except Exception:
            return None

    def update(
        self,
        id: str,
        *,
        name: str = "",
        description: str = "",
        tags: list[str] | None = None,
        metadata: dict[str, str] | None = None,
    ) -> Dataset:
        """Update a dataset."""
        request = datasets_pb2.UpdateDatasetRequest(
            id=id,
            name=name,
            description=description,
            tags=tags or [],
            metadata=metadata or {},
        )
        response = self.stub.UpdateDataset(request, timeout=self._timeout)
        return self._to_dataset(response.dataset)

    def delete(self, id: str) -> bool:
        """Delete a dataset."""
        request = datasets_pb2.DeleteDatasetRequest(id=id)
        response = self.stub.DeleteDataset(request, timeout=self._timeout)
        return response.success

    def list(
        self,
        *,
        prompt_id: str = "",
        tags: list[str] | None = None,
        search: str = "",
        limit: int = 100,
        offset: int = 0,
    ) -> tuple[list[Dataset], int]:
        """List datasets."""
        request = datasets_pb2.ListDatasetsRequest(
            prompt_id=prompt_id,
            tags=tags or [],
            search=search,
            limit=limit,
            offset=offset,
        )
        response = self.stub.ListDatasets(request, timeout=self._timeout)
        datasets = [self._to_dataset(d) for d in response.datasets]
        return datasets, response.total_count

    def add_examples(
        self,
        dataset_id: str,
        examples: list[ExampleInput],
    ) -> tuple[list[Example], int]:
        """Add examples to a dataset."""
        pb_examples = []
        for ex in examples:
            pb_ex = datasets_pb2.ExampleInput(
                input=self._dict_to_struct(ex.input),
                expected_output=self._dict_to_struct(ex.expected_output),
                metadata=ex.metadata,
                source=self._source_to_pb(ex.source),
            )
            pb_examples.append(pb_ex)

        request = datasets_pb2.AddExamplesRequest(
            dataset_id=dataset_id,
            examples=pb_examples,
        )
        response = self.stub.AddExamples(request, timeout=self._timeout)
        examples_out = [self._to_example(e) for e in response.examples]
        return examples_out, response.added_count

    def get_examples(
        self,
        dataset_id: str,
        *,
        limit: int = 100,
        offset: int = 0,
        shuffle: bool = False,
    ) -> tuple[list[Example], int]:
        """Get examples from a dataset."""
        request = datasets_pb2.GetExamplesRequest(
            dataset_id=dataset_id,
            limit=limit,
            offset=offset,
            shuffle=shuffle,
        )
        response = self.stub.GetExamples(request, timeout=self._timeout)
        examples = [self._to_example(e) for e in response.examples]
        return examples, response.total_count

    def remove_examples(self, dataset_id: str, example_ids: list[str]) -> int:
        """Remove examples from a dataset."""
        request = datasets_pb2.RemoveExamplesRequest(
            dataset_id=dataset_id,
            example_ids=example_ids,
        )
        response = self.stub.RemoveExamples(request, timeout=self._timeout)
        return response.removed_count

    def _to_dataset(self, pb: datasets_pb2.Dataset) -> Dataset:
        """Convert protobuf to model."""
        schema = None
        if pb.HasField("schema"):
            schema = DatasetSchema(
                input_fields=[
                    SchemaField(
                        name=f.name,
                        type=f.type,
                        description=f.description,
                        required=f.required,
                    )
                    for f in pb.schema.input_fields
                ],
                expected_output_fields=[
                    SchemaField(
                        name=f.name,
                        type=f.type,
                        description=f.description,
                        required=f.required,
                    )
                    for f in pb.schema.expected_output_fields
                ],
            )

        return Dataset(
            id=pb.id,
            name=pb.name,
            description=pb.description,
            prompt_id=pb.prompt_id,
            schema_=schema,
            example_count=pb.example_count,
            last_updated=pb.last_updated.ToDatetime(timezone.utc) if pb.HasField("last_updated") else None,
            tags=list(pb.tags),
            metadata=dict(pb.metadata),
            version=pb.version,
            created_by=pb.created_by,
            created_at=pb.created_at.ToDatetime(timezone.utc) if pb.HasField("created_at") else None,
        )

    def _to_example(self, pb: datasets_pb2.Example) -> Example:
        """Convert protobuf example to model."""
        return Example(
            id=pb.id,
            dataset_id=pb.dataset_id,
            input=self._struct_to_dict(pb.input),
            expected_output=self._struct_to_dict(pb.expected_output),
            metadata=dict(pb.metadata),
            source=self._source_from_pb(pb.source),
            created_at=pb.created_at.ToDatetime(timezone.utc) if pb.HasField("created_at") else None,
        )

    def _dict_to_struct(self, d: dict[str, Any]) -> struct_pb2.Struct:
        """Convert dict to protobuf Struct."""
        struct = struct_pb2.Struct()
        struct.update(d)
        return struct

    def _struct_to_dict(self, struct: struct_pb2.Struct) -> dict[str, Any]:
        """Convert protobuf Struct to dict."""
        from google.protobuf.json_format import MessageToDict
        return MessageToDict(struct)

    def _source_to_pb(self, source: ExampleSource) -> int:
        """Convert ExampleSource to protobuf."""
        mapping = {
            ExampleSource.UNSPECIFIED: datasets_pb2.EXAMPLE_SOURCE_UNSPECIFIED,
            ExampleSource.MANUAL: datasets_pb2.EXAMPLE_SOURCE_MANUAL,
            ExampleSource.GENERATED: datasets_pb2.EXAMPLE_SOURCE_GENERATED,
            ExampleSource.PRODUCTION: datasets_pb2.EXAMPLE_SOURCE_PRODUCTION,
            ExampleSource.IMPORTED: datasets_pb2.EXAMPLE_SOURCE_IMPORTED,
        }
        return mapping.get(source, datasets_pb2.EXAMPLE_SOURCE_UNSPECIFIED)

    def _source_from_pb(self, source: int) -> ExampleSource:
        """Convert protobuf to ExampleSource."""
        mapping = {
            datasets_pb2.EXAMPLE_SOURCE_UNSPECIFIED: ExampleSource.UNSPECIFIED,
            datasets_pb2.EXAMPLE_SOURCE_MANUAL: ExampleSource.MANUAL,
            datasets_pb2.EXAMPLE_SOURCE_GENERATED: ExampleSource.GENERATED,
            datasets_pb2.EXAMPLE_SOURCE_PRODUCTION: ExampleSource.PRODUCTION,
            datasets_pb2.EXAMPLE_SOURCE_IMPORTED: ExampleSource.IMPORTED,
        }
        return mapping.get(source, ExampleSource.UNSPECIFIED)
