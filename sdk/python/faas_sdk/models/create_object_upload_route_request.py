from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateObjectUploadRouteRequest")


@_attrs_define
class CreateObjectUploadRouteRequest:
    """Upload policy declaration for POST /uploads/{name}."""

    name: str
    bucket_id: UUID
    key_prefix: str | Unset = UNSET
    max_bytes: int | Unset = UNSET
    allowed_content_types: list[str] | Unset = UNSET
    enabled: bool | Unset = True

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        bucket_id = str(self.bucket_id)

        key_prefix = self.key_prefix

        max_bytes = self.max_bytes

        allowed_content_types: list[str] | Unset = UNSET
        if not isinstance(self.allowed_content_types, Unset):
            allowed_content_types = self.allowed_content_types

        enabled = self.enabled

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "bucket_id": bucket_id,
            }
        )
        if key_prefix is not UNSET:
            field_dict["key_prefix"] = key_prefix
        if max_bytes is not UNSET:
            field_dict["max_bytes"] = max_bytes
        if allowed_content_types is not UNSET:
            field_dict["allowed_content_types"] = allowed_content_types
        if enabled is not UNSET:
            field_dict["enabled"] = enabled

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        bucket_id = UUID(d.pop("bucket_id"))

        key_prefix = d.pop("key_prefix", UNSET)

        max_bytes = d.pop("max_bytes", UNSET)

        allowed_content_types = cast(list[str], d.pop("allowed_content_types", UNSET))

        enabled = d.pop("enabled", UNSET)

        create_object_upload_route_request = cls(
            name=name,
            bucket_id=bucket_id,
            key_prefix=key_prefix,
            max_bytes=max_bytes,
            allowed_content_types=allowed_content_types,
            enabled=enabled,
        )

        return create_object_upload_route_request
