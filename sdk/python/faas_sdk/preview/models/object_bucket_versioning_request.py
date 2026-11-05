from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.object_bucket_versioning_request_status import (
    ObjectBucketVersioningRequestStatus,
    check_object_bucket_versioning_request_status,
)

T = TypeVar("T", bound="ObjectBucketVersioningRequest")


@_attrs_define
class ObjectBucketVersioningRequest:
    """Desired Enabled or Suspended status for a durable bucket configuration cutover."""

    status: ObjectBucketVersioningRequestStatus

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "status": status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_object_bucket_versioning_request_status(d.pop("status"))

        object_bucket_versioning_request = cls(
            status=status,
        )

        return object_bucket_versioning_request
