from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ListBucketObjectsResponse200ItemsItem")


@_attrs_define
class ListBucketObjectsResponse200ItemsItem:
    key: str
    size_bytes: int
    last_modified: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        size_bytes = self.size_bytes

        last_modified = self.last_modified.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
                "size_bytes": size_bytes,
                "last_modified": last_modified,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key = d.pop("key")

        size_bytes = d.pop("size_bytes")

        last_modified = datetime.datetime.fromisoformat(d.pop("last_modified"))

        list_bucket_objects_response_200_items_item = cls(
            key=key,
            size_bytes=size_bytes,
            last_modified=last_modified,
        )

        list_bucket_objects_response_200_items_item.additional_properties = d
        return list_bucket_objects_response_200_items_item

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
