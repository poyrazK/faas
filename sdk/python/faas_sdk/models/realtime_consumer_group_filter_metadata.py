from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RealtimeConsumerGroupFilterMetadata")


@_attrs_define
class RealtimeConsumerGroupFilterMetadata:
    """Exact case-sensitive string equality; valid metadata keys use lowercase letters, digits, underscore, hyphen or dot
    and are 1 to 64 bytes. Values are at most 128 bytes and cannot contain NUL, CR or LF. Encoded metadata is bounded to
    4096 bytes.

    """

    additional_properties: dict[str, str] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        realtime_consumer_group_filter_metadata = cls()

        realtime_consumer_group_filter_metadata.additional_properties = d
        return realtime_consumer_group_filter_metadata

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> str:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: str) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
