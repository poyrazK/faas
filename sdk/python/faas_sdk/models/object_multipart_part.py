from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="ObjectMultipartPart")


@_attrs_define
class ObjectMultipartPart:
    """A part confirmed by the upstream object storage provider."""

    part_number: int
    etag: str
    size_bytes: int
    last_modified: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        part_number = self.part_number

        etag = self.etag

        size_bytes = self.size_bytes

        last_modified = self.last_modified.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "part_number": part_number,
                "etag": etag,
                "size_bytes": size_bytes,
                "last_modified": last_modified,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        part_number = d.pop("part_number")

        etag = d.pop("etag")

        size_bytes = d.pop("size_bytes")

        last_modified = datetime.datetime.fromisoformat(d.pop("last_modified"))

        object_multipart_part = cls(
            part_number=part_number,
            etag=etag,
            size_bytes=size_bytes,
            last_modified=last_modified,
        )

        return object_multipart_part
