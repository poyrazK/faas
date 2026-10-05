from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="PreviewEnvironmentGitRevisionRequest")


@_attrs_define
class PreviewEnvironmentGitRevisionRequest:
    """An exact GitHub commit to fetch and review."""

    commit_sha: str

    def to_dict(self) -> dict[str, Any]:
        commit_sha = self.commit_sha

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "commit_sha": commit_sha,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        commit_sha = d.pop("commit_sha")

        preview_environment_git_revision_request = cls(
            commit_sha=commit_sha,
        )

        return preview_environment_git_revision_request
