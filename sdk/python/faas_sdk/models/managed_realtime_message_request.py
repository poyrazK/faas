from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedRealtimeMessageRequest")


@_attrs_define
class ManagedRealtimeMessageRequest:
    """Binary-safe message payload encoded as standard base64."""

    data_base64: str
    """Decoded payload is limited to 1 MiB."""
    binary: bool | Unset = False
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        data_base64 = self.data_base64

        binary = self.binary

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "data_base64": data_base64,
            }
        )
        if binary is not UNSET:
            field_dict["binary"] = binary

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        data_base64 = d.pop("data_base64")

        binary = d.pop("binary", UNSET)

        managed_realtime_message_request = cls(
            data_base64=data_base64,
            binary=binary,
        )

        managed_realtime_message_request.additional_properties = d
        return managed_realtime_message_request

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
