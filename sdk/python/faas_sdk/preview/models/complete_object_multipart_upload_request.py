from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.object_multipart_completed_part import ObjectMultipartCompletedPart


T = TypeVar("T", bound="CompleteObjectMultipartUploadRequest")


@_attrs_define
class CompleteObjectMultipartUploadRequest:
    """Complete ordered ETag manifest for every part in the session."""

    parts: list[ObjectMultipartCompletedPart]

    def to_dict(self) -> dict[str, Any]:
        parts = []
        for parts_item_data in self.parts:
            parts_item = parts_item_data.to_dict()
            parts.append(parts_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "parts": parts,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_multipart_completed_part import ObjectMultipartCompletedPart

        d = dict(src_dict)
        parts = []
        _parts = d.pop("parts")
        for parts_item_data in _parts:
            parts_item = ObjectMultipartCompletedPart.from_dict(parts_item_data)

            parts.append(parts_item)

        complete_object_multipart_upload_request = cls(
            parts=parts,
        )

        return complete_object_multipart_upload_request
