from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="EnvironmentGitProtectedBranchEvidence")


@_attrs_define
class EnvironmentGitProtectedBranchEvidence:
    """Protected branch policy and approval checks bound to the exact environment definition commit."""

    qualified: bool
    profile: str
    installation_id: int
    repository_id: int
    repository: str
    branch: str
    commit_sha: str
    policy_digest: str
    required_review_count: int
    checked_at: datetime.datetime
    reason: str | Unset = UNSET
    """Protected branch check failure reason; excluded from durable approval evidence."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        qualified = self.qualified

        profile = self.profile

        installation_id = self.installation_id

        repository_id = self.repository_id

        repository = self.repository

        branch = self.branch

        commit_sha = self.commit_sha

        policy_digest = self.policy_digest

        required_review_count = self.required_review_count

        checked_at = self.checked_at.isoformat()

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "qualified": qualified,
                "profile": profile,
                "installation_id": installation_id,
                "repository_id": repository_id,
                "repository": repository,
                "branch": branch,
                "commit_sha": commit_sha,
                "policy_digest": policy_digest,
                "required_review_count": required_review_count,
                "checked_at": checked_at,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        qualified = d.pop("qualified")

        profile = d.pop("profile")

        installation_id = d.pop("installation_id")

        repository_id = d.pop("repository_id")

        repository = d.pop("repository")

        branch = d.pop("branch")

        commit_sha = d.pop("commit_sha")

        policy_digest = d.pop("policy_digest")

        required_review_count = d.pop("required_review_count")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        reason = d.pop("reason", UNSET)

        environment_git_protected_branch_evidence = cls(
            qualified=qualified,
            profile=profile,
            installation_id=installation_id,
            repository_id=repository_id,
            repository=repository,
            branch=branch,
            commit_sha=commit_sha,
            policy_digest=policy_digest,
            required_review_count=required_review_count,
            checked_at=checked_at,
            reason=reason,
        )

        environment_git_protected_branch_evidence.additional_properties = d
        return environment_git_protected_branch_evidence

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
