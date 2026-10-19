from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="ApproveApplicationStandardReviewRequest")


@_attrs_define
class ApproveApplicationStandardReviewRequest:
    """Exact approval hash of the saved review whose authoritative inputs must still match."""

    approval_hash: str

    def to_dict(self) -> dict[str, Any]:
        approval_hash = self.approval_hash

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "approval_hash": approval_hash,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        approval_hash = d.pop("approval_hash")

        approve_application_standard_review_request = cls(
            approval_hash=approval_hash,
        )

        return approve_application_standard_review_request
