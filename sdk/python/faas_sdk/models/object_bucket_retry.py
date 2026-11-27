from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.object_bucket_retry_reason import ObjectBucketRetryReason, check_object_bucket_retry_reason

T = TypeVar("T", bound="ObjectBucketRetry")


@_attrs_define
class ObjectBucketRetry:
    """Set while a bucket create or delete has failed and Gregale is retrying it. Reason is a bounded code, never a
    provider message.

    """

    reason: ObjectBucketRetryReason
    detail: str
    """Customer-facing explanation of the reason."""
    attempts: int
    retry_at: datetime.datetime
    """When Gregale next tries the operation."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        reason: str = self.reason

        detail = self.detail

        attempts = self.attempts

        retry_at = self.retry_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "reason": reason,
                "detail": detail,
                "attempts": attempts,
                "retry_at": retry_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        reason = check_object_bucket_retry_reason(d.pop("reason"))

        detail = d.pop("detail")

        attempts = d.pop("attempts")

        retry_at = datetime.datetime.fromisoformat(d.pop("retry_at"))

        object_bucket_retry = cls(
            reason=reason,
            detail=detail,
            attempts=attempts,
            retry_at=retry_at,
        )

        object_bucket_retry.additional_properties = d
        return object_bucket_retry

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
