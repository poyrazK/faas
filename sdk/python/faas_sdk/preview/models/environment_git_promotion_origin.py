from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.environment_git_promotion_origin_provider import (
    EnvironmentGitPromotionOriginProvider,
    check_environment_git_promotion_origin_provider,
)

T = TypeVar("T", bound="EnvironmentGitPromotionOrigin")


@_attrs_define
class EnvironmentGitPromotionOrigin:
    """Scoped identity of the approved source release; graph and release IDs are empty if unproven."""

    source_id: str
    """Source binding identity."""
    revision_id: str
    """Current approved revision identity."""
    commit_sha: str
    """Approved immutable source commit."""
    definition_digest: str
    """Approved source definition digest."""
    generation: int
    """Source approval generation."""
    intent_version: int
    """Observed source intent version."""
    graph_id: str
    """Current qualified graph identity, if present."""
    release_set_id: str
    """Active source release identity, if present."""
    provider: EnvironmentGitPromotionOriginProvider
    """Bound provider."""
    repository_id: int
    """Immutable provider repository identity."""

    def to_dict(self) -> dict[str, Any]:
        source_id = self.source_id

        revision_id = self.revision_id

        commit_sha = self.commit_sha

        definition_digest = self.definition_digest

        generation = self.generation

        intent_version = self.intent_version

        graph_id = self.graph_id

        release_set_id = self.release_set_id

        provider: str = self.provider

        repository_id = self.repository_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "source_id": source_id,
                "revision_id": revision_id,
                "commit_sha": commit_sha,
                "definition_digest": definition_digest,
                "generation": generation,
                "intent_version": intent_version,
                "graph_id": graph_id,
                "release_set_id": release_set_id,
                "provider": provider,
                "repository_id": repository_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source_id = d.pop("source_id")

        revision_id = d.pop("revision_id")

        commit_sha = d.pop("commit_sha")

        definition_digest = d.pop("definition_digest")

        generation = d.pop("generation")

        intent_version = d.pop("intent_version")

        graph_id = d.pop("graph_id")

        release_set_id = d.pop("release_set_id")

        provider = check_environment_git_promotion_origin_provider(d.pop("provider"))

        repository_id = d.pop("repository_id")

        environment_git_promotion_origin = cls(
            source_id=source_id,
            revision_id=revision_id,
            commit_sha=commit_sha,
            definition_digest=definition_digest,
            generation=generation,
            intent_version=intent_version,
            graph_id=graph_id,
            release_set_id=release_set_id,
            provider=provider,
            repository_id=repository_id,
        )

        return environment_git_promotion_origin
