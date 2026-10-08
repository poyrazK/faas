from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ObjectVersion")


@_attrs_define
class ObjectVersion:
    """Logical object data and an owned public version selector; native provider identifiers remain private."""

    key: str
    version_id: str
    is_latest: bool
    delete_marker: bool
    size_bytes: int
    last_modified: datetime.datetime
    etag: str | Unset = UNSET
    storage_class: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        version_id = self.version_id

        is_latest = self.is_latest

        delete_marker = self.delete_marker

        size_bytes = self.size_bytes

        last_modified = self.last_modified.isoformat()

        etag = self.etag

        storage_class = self.storage_class

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "key": key,
                "version_id": version_id,
                "is_latest": is_latest,
                "delete_marker": delete_marker,
                "size_bytes": size_bytes,
                "last_modified": last_modified,
            }
        )
        if etag is not UNSET:
            field_dict["etag"] = etag
        if storage_class is not UNSET:
            field_dict["storage_class"] = storage_class

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key = d.pop("key")

        version_id = d.pop("version_id")

        is_latest = d.pop("is_latest")

        delete_marker = d.pop("delete_marker")

        size_bytes = d.pop("size_bytes")

        last_modified = datetime.datetime.fromisoformat(d.pop("last_modified"))

        etag = d.pop("etag", UNSET)

        storage_class = d.pop("storage_class", UNSET)

        object_version = cls(
            key=key,
            version_id=version_id,
            is_latest=is_latest,
            delete_marker=delete_marker,
            size_bytes=size_bytes,
            last_modified=last_modified,
            etag=etag,
            storage_class=storage_class,
        )

        return object_version
