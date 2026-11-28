from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="PreviewEnvironmentGitPromotionRequest")


@_attrs_define
class PreviewEnvironmentGitPromotionRequest:
    """Read-only environment promotion observation. A saved hash is not approval."""

    from_environment: str
    """Distinct source environment slug."""
    target_commit_sha: str
    """Verified commit containing the target environment manifest."""
    expected_plan_hash: str | Unset = UNSET
    """Previously reviewed hash; changed source, release, artifact or target observations return 409."""

    def to_dict(self) -> dict[str, Any]:
        from_environment = self.from_environment

        target_commit_sha = self.target_commit_sha

        expected_plan_hash = self.expected_plan_hash

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "from_environment": from_environment,
                "target_commit_sha": target_commit_sha,
            }
        )
        if expected_plan_hash is not UNSET:
            field_dict["expected_plan_hash"] = expected_plan_hash

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        from_environment = d.pop("from_environment")

        target_commit_sha = d.pop("target_commit_sha")

        expected_plan_hash = d.pop("expected_plan_hash", UNSET)

        preview_environment_git_promotion_request = cls(
            from_environment=from_environment,
            target_commit_sha=target_commit_sha,
            expected_plan_hash=expected_plan_hash,
        )

        return preview_environment_git_promotion_request
