from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.object_bucket_access_grant_key_status import (
    ObjectBucketAccessGrantKeyStatus,
    check_object_bucket_access_grant_key_status,
)
from ..models.object_bucket_access_grant_permission import (
    ObjectBucketAccessGrantPermission,
    check_object_bucket_access_grant_permission,
)

T = TypeVar("T", bound="ObjectBucketAccessGrant")


@_attrs_define
class ObjectBucketAccessGrant:
    """Provider-independent access binding between one Gregale API key and one logical bucket."""

    key_id: UUID
    key_label: str
    key_status: ObjectBucketAccessGrantKeyStatus
    permission: ObjectBucketAccessGrantPermission
    created_at: datetime.datetime
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key_id = str(self.key_id)

        key_label = self.key_label

        key_status: str = self.key_status

        permission: str = self.permission

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key_id": key_id,
                "key_label": key_label,
                "key_status": key_status,
                "permission": permission,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key_id = UUID(d.pop("key_id"))

        key_label = d.pop("key_label")

        key_status = check_object_bucket_access_grant_key_status(d.pop("key_status"))

        permission = check_object_bucket_access_grant_permission(d.pop("permission"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        object_bucket_access_grant = cls(
            key_id=key_id,
            key_label=key_label,
            key_status=key_status,
            permission=permission,
            created_at=created_at,
            updated_at=updated_at,
        )

        object_bucket_access_grant.additional_properties = d
        return object_bucket_access_grant

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
