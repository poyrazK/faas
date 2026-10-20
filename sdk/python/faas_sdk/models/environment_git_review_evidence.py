from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="EnvironmentGitReviewEvidence")


@_attrs_define
class EnvironmentGitReviewEvidence:
    """One reviewer decision for the proposed environment definition at an exact commit."""

    id: int
    reviewer_id: int
    reviewer: str
    head_sha: str
    submitted_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        reviewer_id = self.reviewer_id

        reviewer = self.reviewer

        head_sha = self.head_sha

        submitted_at = self.submitted_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "reviewer_id": reviewer_id,
                "reviewer": reviewer,
                "head_sha": head_sha,
                "submitted_at": submitted_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        reviewer_id = d.pop("reviewer_id")

        reviewer = d.pop("reviewer")

        head_sha = d.pop("head_sha")

        submitted_at = datetime.datetime.fromisoformat(d.pop("submitted_at"))

        environment_git_review_evidence = cls(
            id=id,
            reviewer_id=reviewer_id,
            reviewer=reviewer,
            head_sha=head_sha,
            submitted_at=submitted_at,
        )

        environment_git_review_evidence.additional_properties = d
        return environment_git_review_evidence

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
