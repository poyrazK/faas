from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ObjectUploadRoute")


@_attrs_define
class ObjectUploadRoute:
    """Policy-controlled edge upload route. The edge generates the final object key and never exposes provider placement."""

    id: UUID
    name: str
    bucket_id: UUID
    max_bytes: int
    enabled: bool
    created_at: datetime.datetime
    updated_at: datetime.datetime
    key_prefix: str | Unset = UNSET
    allowed_content_types: list[str] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        bucket_id = str(self.bucket_id)

        max_bytes = self.max_bytes

        enabled = self.enabled

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        key_prefix = self.key_prefix

        allowed_content_types: list[str] | Unset = UNSET
        if not isinstance(self.allowed_content_types, Unset):
            allowed_content_types = self.allowed_content_types

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "bucket_id": bucket_id,
                "max_bytes": max_bytes,
                "enabled": enabled,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if key_prefix is not UNSET:
            field_dict["key_prefix"] = key_prefix
        if allowed_content_types is not UNSET:
            field_dict["allowed_content_types"] = allowed_content_types

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        bucket_id = UUID(d.pop("bucket_id"))

        max_bytes = d.pop("max_bytes")

        enabled = d.pop("enabled")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        key_prefix = d.pop("key_prefix", UNSET)

        allowed_content_types = cast(list[str], d.pop("allowed_content_types", UNSET))

        object_upload_route = cls(
            id=id,
            name=name,
            bucket_id=bucket_id,
            max_bytes=max_bytes,
            enabled=enabled,
            created_at=created_at,
            updated_at=updated_at,
            key_prefix=key_prefix,
            allowed_content_types=allowed_content_types,
        )

        object_upload_route.additional_properties = d
        return object_upload_route

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
