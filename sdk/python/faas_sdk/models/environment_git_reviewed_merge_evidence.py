from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.environment_git_protected_branch_evidence import EnvironmentGitProtectedBranchEvidence
    from ..models.environment_git_review_evidence import EnvironmentGitReviewEvidence


T = TypeVar("T", bound="EnvironmentGitReviewedMergeEvidence")


@_attrs_define
class EnvironmentGitReviewedMergeEvidence:
    """Reviews of the final PR head before merge. Permissions and protection describe the check time, not historical
    policy.

    """

    reviewed_definition_digest: str
    qualified: bool
    profile: str
    policy: EnvironmentGitProtectedBranchEvidence
    pull_request_id: int
    pull_request_number: int
    author_id: int
    head_sha: str
    merged_at: datetime.datetime
    reviews: list[EnvironmentGitReviewEvidence]
    checked_at: datetime.datetime
    reason: str | Unset = UNSET
    """Qualification failure reason; absent from persisted approval evidence."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        reviewed_definition_digest = self.reviewed_definition_digest

        qualified = self.qualified

        profile = self.profile

        policy = self.policy.to_dict()

        pull_request_id = self.pull_request_id

        pull_request_number = self.pull_request_number

        author_id = self.author_id

        head_sha = self.head_sha

        merged_at = self.merged_at.isoformat()

        reviews = []
        for reviews_item_data in self.reviews:
            reviews_item = reviews_item_data.to_dict()
            reviews.append(reviews_item)

        checked_at = self.checked_at.isoformat()

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "reviewed_definition_digest": reviewed_definition_digest,
                "qualified": qualified,
                "profile": profile,
                "policy": policy,
                "pull_request_id": pull_request_id,
                "pull_request_number": pull_request_number,
                "author_id": author_id,
                "head_sha": head_sha,
                "merged_at": merged_at,
                "reviews": reviews,
                "checked_at": checked_at,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_git_protected_branch_evidence import EnvironmentGitProtectedBranchEvidence
        from ..models.environment_git_review_evidence import EnvironmentGitReviewEvidence

        d = dict(src_dict)
        reviewed_definition_digest = d.pop("reviewed_definition_digest")

        qualified = d.pop("qualified")

        profile = d.pop("profile")

        policy = EnvironmentGitProtectedBranchEvidence.from_dict(d.pop("policy"))

        pull_request_id = d.pop("pull_request_id")

        pull_request_number = d.pop("pull_request_number")

        author_id = d.pop("author_id")

        head_sha = d.pop("head_sha")

        merged_at = datetime.datetime.fromisoformat(d.pop("merged_at"))

        reviews = []
        _reviews = d.pop("reviews")
        for reviews_item_data in _reviews:
            reviews_item = EnvironmentGitReviewEvidence.from_dict(reviews_item_data)

            reviews.append(reviews_item)

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        reason = d.pop("reason", UNSET)

        environment_git_reviewed_merge_evidence = cls(
            reviewed_definition_digest=reviewed_definition_digest,
            qualified=qualified,
            profile=profile,
            policy=policy,
            pull_request_id=pull_request_id,
            pull_request_number=pull_request_number,
            author_id=author_id,
            head_sha=head_sha,
            merged_at=merged_at,
            reviews=reviews,
            checked_at=checked_at,
            reason=reason,
        )

        environment_git_reviewed_merge_evidence.additional_properties = d
        return environment_git_reviewed_merge_evidence

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
