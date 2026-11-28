from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="ApproveEnvironmentGitRevisionRequest")


@_attrs_define
class ApproveEnvironmentGitRevisionRequest:
    """Approve the reviewed digest for an immutable commit using the current source generation."""

    commit_sha: str
    definition_digest: str
    expected_generation: int

    def to_dict(self) -> dict[str, Any]:
        commit_sha = self.commit_sha

        definition_digest = self.definition_digest

        expected_generation = self.expected_generation

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "commit_sha": commit_sha,
                "definition_digest": definition_digest,
                "expected_generation": expected_generation,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        commit_sha = d.pop("commit_sha")

        definition_digest = d.pop("definition_digest")

        expected_generation = d.pop("expected_generation")

        approve_environment_git_revision_request = cls(
            commit_sha=commit_sha,
            definition_digest=definition_digest,
            expected_generation=expected_generation,
        )

        return approve_environment_git_revision_request
