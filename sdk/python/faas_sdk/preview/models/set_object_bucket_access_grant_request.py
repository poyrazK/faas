from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.set_object_bucket_access_grant_request_permission import (
    SetObjectBucketAccessGrantRequestPermission,
    check_set_object_bucket_access_grant_request_permission,
)

T = TypeVar("T", bound="SetObjectBucketAccessGrantRequest")


@_attrs_define
class SetObjectBucketAccessGrantRequest:
    """Desired data-plane permission for the target API key."""

    permission: SetObjectBucketAccessGrantRequestPermission

    def to_dict(self) -> dict[str, Any]:
        permission: str = self.permission

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "permission": permission,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        permission = check_set_object_bucket_access_grant_request_permission(d.pop("permission"))

        set_object_bucket_access_grant_request = cls(
            permission=permission,
        )

        return set_object_bucket_access_grant_request
