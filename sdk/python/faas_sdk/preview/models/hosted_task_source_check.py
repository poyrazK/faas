from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="HostedTaskSourceCheck")


@_attrs_define
class HostedTaskSourceCheck:
    """Freshness observation for the exact retained hosted Task source review, without an owner decision."""

    review_sha256: str
    """Hosted source-review fingerprint to compare with current native facts."""

    def to_dict(self) -> dict[str, Any]:
        review_sha256 = self.review_sha256

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "review_sha256": review_sha256,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        review_sha256 = d.pop("review_sha256")

        hosted_task_source_check = cls(
            review_sha256=review_sha256,
        )

        return hosted_task_source_check
