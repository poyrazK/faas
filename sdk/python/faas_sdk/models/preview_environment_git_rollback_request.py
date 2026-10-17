from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="PreviewEnvironmentGitRollbackRequest")


@_attrs_define
class PreviewEnvironmentGitRollbackRequest:
    """Read-only environment rollback observation. A saved hash is not approval."""

    release_set_id: UUID
    """Retained GitOps serving release UUID in this environment."""
    target_commit_sha: str
    """Immutable commit whose current manifest will receive the rollback source changes."""
    expected_plan_hash: str | Unset = UNSET
    """Previously reviewed rollback hash; changed historical evidence or target intent returns 409."""

    def to_dict(self) -> dict[str, Any]:
        release_set_id = str(self.release_set_id)

        target_commit_sha = self.target_commit_sha

        expected_plan_hash = self.expected_plan_hash

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "release_set_id": release_set_id,
                "target_commit_sha": target_commit_sha,
            }
        )
        if expected_plan_hash is not UNSET:
            field_dict["expected_plan_hash"] = expected_plan_hash

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        release_set_id = UUID(d.pop("release_set_id"))

        target_commit_sha = d.pop("target_commit_sha")

        expected_plan_hash = d.pop("expected_plan_hash", UNSET)

        preview_environment_git_rollback_request = cls(
            release_set_id=release_set_id,
            target_commit_sha=target_commit_sha,
            expected_plan_hash=expected_plan_hash,
        )

        return preview_environment_git_rollback_request
