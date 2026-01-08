"""Client for the prompt service."""

from __future__ import annotations

import sys
from datetime import datetime, timezone
from typing import TYPE_CHECKING

from delos.models.prompt import Prompt, PromptMessage, PromptVariable, PromptVersion
from delos.services.base import BaseClient

if TYPE_CHECKING:
    from delos.config import ServiceEndpoint

# Add gen/python to path for imports
sys.path.insert(0, "gen/python")

try:
    from prompt.v1 import prompt_pb2, prompt_pb2_grpc
except ImportError:
    prompt_pb2 = None  # type: ignore
    prompt_pb2_grpc = None  # type: ignore


class PromptClient(BaseClient):
    """Client for the prompt service."""

    def __init__(self, endpoint: ServiceEndpoint, timeout: float = 30.0) -> None:
        """Initialize the prompt client."""
        super().__init__(endpoint, timeout)
        self._stub: prompt_pb2_grpc.PromptServiceStub | None = None

    @property
    def stub(self) -> prompt_pb2_grpc.PromptServiceStub:
        """Get the gRPC stub."""
        if self._stub is None:
            if prompt_pb2_grpc is None:
                raise ImportError("Generated protobuf code not found. Run 'buf generate' first.")
            self._stub = prompt_pb2_grpc.PromptServiceStub(self.channel)
        return self._stub

    def create(
        self,
        name: str,
        *,
        slug: str = "",
        description: str = "",
        template: str = "",
        system_prompt: str = "",
        messages: list[PromptMessage] | None = None,
        variables: list[PromptVariable] | None = None,
        model: str = "",
        temperature: float = 0.7,
        max_tokens: int = 1024,
        tags: list[str] | None = None,
        metadata: dict[str, str] | None = None,
    ) -> Prompt:
        """Create a new prompt.

        Args:
            name: Display name for the prompt.
            slug: URL-safe identifier (auto-generated if not provided).
            description: Description of the prompt's purpose.
            template: The prompt template with {{variable}} placeholders.
            system_prompt: System prompt for the LLM.
            messages: List of messages for chat-style prompts.
            variables: Variable definitions for the template.
            model: Preferred model for this prompt.
            temperature: Default temperature setting.
            max_tokens: Default max tokens setting.
            tags: Tags for categorization.
            metadata: Additional metadata.

        Returns:
            The created prompt.
        """
        pb_messages = []
        if messages:
            for m in messages:
                pb_messages.append(prompt_pb2.PromptMessage(role=m.role, content=m.content))

        pb_variables = []
        if variables:
            for v in variables:
                pb_variables.append(
                    prompt_pb2.PromptVariable(
                        name=v.name,
                        description=v.description,
                        default_value=v.default_value,
                        required=v.required,
                    )
                )

        request = prompt_pb2.CreatePromptRequest(
            name=name,
            slug=slug,
            description=description,
            template=template,
            system_prompt=system_prompt,
            messages=pb_messages,
            variables=pb_variables,
            model=model,
            temperature=temperature,
            max_tokens=max_tokens,
            tags=tags or [],
            metadata=metadata or {},
        )

        response = self.stub.CreatePrompt(request, timeout=self._timeout)
        return self._to_prompt(response.prompt)

    def get(self, id_or_slug: str, *, version: int | None = None) -> Prompt | None:
        """Get a prompt by ID or slug.

        Args:
            id_or_slug: The prompt ID or slug (e.g., "summarizer" or "summarizer:v2").
            version: Specific version to retrieve (overrides slug version).

        Returns:
            The prompt if found, None otherwise.
        """
        request = prompt_pb2.GetPromptRequest(id=id_or_slug, version=version or 0)
        try:
            response = self.stub.GetPrompt(request, timeout=self._timeout)
            return self._to_prompt(response.prompt)
        except Exception:
            return None

    def update(
        self,
        id: str,
        *,
        template: str | None = None,
        system_prompt: str | None = None,
        messages: list[PromptMessage] | None = None,
        variables: list[PromptVariable] | None = None,
        model: str | None = None,
        temperature: float | None = None,
        max_tokens: int | None = None,
        commit_message: str = "",
    ) -> Prompt:
        """Update a prompt, creating a new version.

        Args:
            id: The prompt ID.
            template: New template (optional).
            system_prompt: New system prompt (optional).
            messages: New messages (optional).
            variables: New variables (optional).
            model: New model (optional).
            temperature: New temperature (optional).
            max_tokens: New max tokens (optional).
            commit_message: Description of the changes.

        Returns:
            The updated prompt with the new version.
        """
        pb_messages = None
        if messages is not None:
            pb_messages = [
                prompt_pb2.PromptMessage(role=m.role, content=m.content) for m in messages
            ]

        pb_variables = None
        if variables is not None:
            pb_variables = [
                prompt_pb2.PromptVariable(
                    name=v.name,
                    description=v.description,
                    default_value=v.default_value,
                    required=v.required,
                )
                for v in variables
            ]

        request = prompt_pb2.UpdatePromptRequest(
            id=id,
            template=template or "",
            system_prompt=system_prompt or "",
            messages=pb_messages or [],
            variables=pb_variables or [],
            model=model or "",
            temperature=temperature or 0.0,
            max_tokens=max_tokens or 0,
            commit_message=commit_message,
        )

        response = self.stub.UpdatePrompt(request, timeout=self._timeout)
        return self._to_prompt(response.prompt)

    def delete(self, id: str) -> bool:
        """Delete a prompt.

        Args:
            id: The prompt ID.

        Returns:
            True if deleted successfully.
        """
        request = prompt_pb2.DeletePromptRequest(id=id)
        response = self.stub.DeletePrompt(request, timeout=self._timeout)
        return response.success

    def list(
        self,
        *,
        tags: list[str] | None = None,
        search: str = "",
        limit: int = 100,
        offset: int = 0,
    ) -> tuple[list[Prompt], int]:
        """List prompts.

        Args:
            tags: Filter by tags.
            search: Search in name and description.
            limit: Maximum number of results.
            offset: Offset for pagination.

        Returns:
            Tuple of (prompts, total_count).
        """
        request = prompt_pb2.ListPromptsRequest(
            tags=tags or [],
            search=search,
            limit=limit,
            offset=offset,
        )

        response = self.stub.ListPrompts(request, timeout=self._timeout)
        prompts = [self._to_prompt(p) for p in response.prompts]
        return prompts, response.total_count

    def get_version(self, id: str, version: int) -> PromptVersion | None:
        """Get a specific version of a prompt.

        Args:
            id: The prompt ID.
            version: The version number.

        Returns:
            The prompt version if found.
        """
        request = prompt_pb2.GetPromptVersionRequest(id=id, version=version)
        try:
            response = self.stub.GetPromptVersion(request, timeout=self._timeout)
            return self._to_version(response.version)
        except Exception:
            return None

    def list_versions(self, id: str) -> list[PromptVersion]:
        """List all versions of a prompt.

        Args:
            id: The prompt ID.

        Returns:
            List of versions.
        """
        request = prompt_pb2.ListVersionsRequest(id=id)
        response = self.stub.ListVersions(request, timeout=self._timeout)
        return [self._to_version(v) for v in response.versions]

    def _to_prompt(self, pb: prompt_pb2.Prompt) -> Prompt:
        """Convert protobuf to model."""
        versions = [self._to_version(v) for v in pb.versions]
        return Prompt(
            id=pb.id,
            name=pb.name,
            slug=pb.slug,
            description=pb.description,
            current_version=pb.current_version,
            versions=versions,
            tags=list(pb.tags),
            metadata=dict(pb.metadata),
            created_at=pb.created_at.ToDatetime(timezone.utc) if pb.HasField("created_at") else None,
            updated_at=pb.updated_at.ToDatetime(timezone.utc) if pb.HasField("updated_at") else None,
            created_by=pb.created_by,
        )

    def _to_version(self, pb: prompt_pb2.PromptVersion) -> PromptVersion:
        """Convert protobuf version to model."""
        messages = [PromptMessage(role=m.role, content=m.content) for m in pb.messages]
        variables = [
            PromptVariable(
                name=v.name,
                description=v.description,
                default_value=v.default_value,
                required=v.required,
            )
            for v in pb.variables
        ]
        return PromptVersion(
            version=pb.version,
            template=pb.template,
            system_prompt=pb.system_prompt,
            messages=messages,
            variables=variables,
            model=pb.model,
            temperature=pb.temperature,
            max_tokens=pb.max_tokens,
            created_at=pb.created_at.ToDatetime(timezone.utc) if pb.HasField("created_at") else None,
            created_by=pb.created_by,
            commit_message=pb.commit_message,
        )
