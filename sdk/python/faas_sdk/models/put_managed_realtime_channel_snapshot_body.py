from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="PutManagedRealtimeChannelSnapshotBody")


@_attrs_define
class PutManagedRealtimeChannelSnapshotBody:
    sequence: int
    data_base64: str
    binary: bool | Unset = False
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sequence = self.sequence

        data_base64 = self.data_base64

        binary = self.binary

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sequence": sequence,
                "data_base64": data_base64,
            }
        )
        if binary is not UNSET:
            field_dict["binary"] = binary

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        sequence = d.pop("sequence")

        data_base64 = d.pop("data_base64")

        binary = d.pop("binary", UNSET)

        put_managed_realtime_channel_snapshot_body = cls(
            sequence=sequence,
            data_base64=data_base64,
            binary=binary,
        )

        put_managed_realtime_channel_snapshot_body.additional_properties = d
        return put_managed_realtime_channel_snapshot_body

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
