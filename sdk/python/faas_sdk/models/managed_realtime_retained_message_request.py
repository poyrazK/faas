from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedRealtimeRetainedMessageRequest")


@_attrs_define
class ManagedRealtimeRetainedMessageRequest:
    """Binary-safe payload to append to retained channel history."""

    data_base64: str
    """Standard base64 for at most 4096 decoded bytes."""
    binary: bool | Unset = False
    idempotency_key: str | Unset = UNSET
    """Deduplicates identical writes while retained."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        data_base64 = self.data_base64

        binary = self.binary

        idempotency_key = self.idempotency_key

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "data_base64": data_base64,
            }
        )
        if binary is not UNSET:
            field_dict["binary"] = binary
        if idempotency_key is not UNSET:
            field_dict["idempotency_key"] = idempotency_key

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        data_base64 = d.pop("data_base64")

        binary = d.pop("binary", UNSET)

        idempotency_key = d.pop("idempotency_key", UNSET)

        managed_realtime_retained_message_request = cls(
            data_base64=data_base64,
            binary=binary,
            idempotency_key=idempotency_key,
        )

        managed_realtime_retained_message_request.additional_properties = d
        return managed_realtime_retained_message_request

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
