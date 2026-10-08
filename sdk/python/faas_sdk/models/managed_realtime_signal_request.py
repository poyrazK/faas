from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedRealtimeSignalRequest")


@_attrs_define
class ManagedRealtimeSignalRequest:
    data: Any
    """Any JSON value, including null, within 2048 encoded UTF-8 bytes."""
    name: str | Unset = UNSET
    ttl_ms: int | Unset = UNSET
    """Requires name; defaults to 5000 for named signals. Zero clears."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        data = self.data

        name = self.name

        ttl_ms = self.ttl_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "data": data,
            }
        )
        if name is not UNSET:
            field_dict["name"] = name
        if ttl_ms is not UNSET:
            field_dict["ttl_ms"] = ttl_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        data = d.pop("data")

        name = d.pop("name", UNSET)

        ttl_ms = d.pop("ttl_ms", UNSET)

        managed_realtime_signal_request = cls(
            data=data,
            name=name,
            ttl_ms=ttl_ms,
        )

        managed_realtime_signal_request.additional_properties = d
        return managed_realtime_signal_request

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
