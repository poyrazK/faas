from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateObjectMultipartUploadRequest")


@_attrs_define
class CreateObjectMultipartUploadRequest:
    """Final object identity and total size for a resumable upload."""

    key: str
    size_bytes: int
    content_type: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        size_bytes = self.size_bytes

        content_type = self.content_type

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "key": key,
                "size_bytes": size_bytes,
            }
        )
        if content_type is not UNSET:
            field_dict["content_type"] = content_type

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key = d.pop("key")

        size_bytes = d.pop("size_bytes")

        content_type = d.pop("content_type", UNSET)

        create_object_multipart_upload_request = cls(
            key=key,
            size_bytes=size_bytes,
            content_type=content_type,
        )

        return create_object_multipart_upload_request
