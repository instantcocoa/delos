"""Models for the prompt service."""

from __future__ import annotations

from datetime import datetime

from pydantic import BaseModel


class PromptVariable(BaseModel):
    """A variable used in a prompt template."""

    name: str
    description: str = ""
    default_value: str = ""
    required: bool = True


class PromptMessage(BaseModel):
    """A message in a prompt template."""

    role: str  # system, user, assistant
    content: str


class PromptVersion(BaseModel):
    """A specific version of a prompt."""

    version: int
    template: str = ""
    system_prompt: str = ""
    messages: list[PromptMessage] = []
    variables: list[PromptVariable] = []
    model: str = ""
    temperature: float = 0.7
    max_tokens: int = 1024
    created_at: datetime | None = None
    created_by: str = ""
    commit_message: str = ""


class Prompt(BaseModel):
    """A prompt with its current and historical versions."""

    id: str
    name: str
    slug: str = ""
    description: str = ""
    current_version: int = 1
    versions: list[PromptVersion] = []
    tags: list[str] = []
    metadata: dict[str, str] = {}
    created_at: datetime | None = None
    updated_at: datetime | None = None
    created_by: str = ""

    def get_version(self, version: int | None = None) -> PromptVersion | None:
        """Get a specific version of the prompt."""
        target = version or self.current_version
        for v in self.versions:
            if v.version == target:
                return v
        return None

    @property
    def latest(self) -> PromptVersion | None:
        """Get the latest version of the prompt."""
        return self.get_version(self.current_version)

    def render(self, variables: dict[str, str] | None = None, version: int | None = None) -> str:
        """Render the prompt template with the given variables."""
        v = self.get_version(version)
        if v is None:
            raise ValueError(f"Version {version or self.current_version} not found")

        template = v.template
        if variables:
            for name, value in variables.items():
                template = template.replace(f"{{{{{name}}}}}", value)
        return template
