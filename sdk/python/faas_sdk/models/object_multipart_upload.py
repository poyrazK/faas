from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.object_multipart_upload_state import ObjectMultipartUploadState, check_object_multipart_upload_state

T = TypeVar("T", bound="ObjectMultipartUpload")


@_attrs_define
class ObjectMultipartUpload:
    """Durable provider-neutral resumable upload session. The provider upload ID is private."""

    id: UUID
    key: str
    size_bytes: int
    part_size_bytes: int
    part_count: int
    content_type: str
    state: ObjectMultipartUploadState
    expires_at: datetime.datetime
    created_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        key = self.key

        size_bytes = self.size_bytes

        part_size_bytes = self.part_size_bytes

        part_count = self.part_count

        content_type = self.content_type

        state: str = self.state

        expires_at = self.expires_at.isoformat()

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "key": key,
                "size_bytes": size_bytes,
                "part_size_bytes": part_size_bytes,
                "part_count": part_count,
                "content_type": content_type,
                "state": state,
                "expires_at": expires_at,
                "created_at": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        key = d.pop("key")

        size_bytes = d.pop("size_bytes")

        part_size_bytes = d.pop("part_size_bytes")

        part_count = d.pop("part_count")

        content_type = d.pop("content_type")

        state = check_object_multipart_upload_state(d.pop("state"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        object_multipart_upload = cls(
            id=id,
            key=key,
            size_bytes=size_bytes,
            part_size_bytes=part_size_bytes,
            part_count=part_count,
            content_type=content_type,
            state=state,
            expires_at=expires_at,
            created_at=created_at,
        )

        object_multipart_upload.additional_properties = d
        return object_multipart_upload

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
