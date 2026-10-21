from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="EnvironmentGitPromotionTarget")


@_attrs_define
class EnvironmentGitPromotionTarget:
    """Target binding and manifest observation; no target values are returned."""

    source_id: str
    """Target source binding identity."""
    generation: int
    """Target source generation."""
    intent_version: int
    """Observed target intent version."""
    commit_sha: str
    """Immutable target manifest commit."""
    definition_digest: str
    """Required local base definition digest."""
    proposed_digest: str
    """Digest after replacing only proposed image source contracts."""
    manifest_path: str
    """Bound target manifest path in Git."""

    def to_dict(self) -> dict[str, Any]:
        source_id = self.source_id

        generation = self.generation

        intent_version = self.intent_version

        commit_sha = self.commit_sha

        definition_digest = self.definition_digest

        proposed_digest = self.proposed_digest

        manifest_path = self.manifest_path

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "source_id": source_id,
                "generation": generation,
                "intent_version": intent_version,
                "commit_sha": commit_sha,
                "definition_digest": definition_digest,
                "proposed_digest": proposed_digest,
                "manifest_path": manifest_path,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source_id = d.pop("source_id")

        generation = d.pop("generation")

        intent_version = d.pop("intent_version")

        commit_sha = d.pop("commit_sha")

        definition_digest = d.pop("definition_digest")

        proposed_digest = d.pop("proposed_digest")

        manifest_path = d.pop("manifest_path")

        environment_git_promotion_target = cls(
            source_id=source_id,
            generation=generation,
            intent_version=intent_version,
            commit_sha=commit_sha,
            definition_digest=definition_digest,
            proposed_digest=proposed_digest,
            manifest_path=manifest_path,
        )

        return environment_git_promotion_target
