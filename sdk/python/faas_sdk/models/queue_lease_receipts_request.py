from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="QueueLeaseReceiptsRequest")


@_attrs_define
class QueueLeaseReceiptsRequest:
    """Receipts for ack, nack or extend. delay_seconds and error apply to nack; visibility_timeout_seconds to extend."""

    receipts: list[str]
    delay_seconds: int | Unset = UNSET
    error: str | Unset = UNSET
    visibility_timeout_seconds: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        receipts = self.receipts

        delay_seconds = self.delay_seconds

        error = self.error

        visibility_timeout_seconds = self.visibility_timeout_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "receipts": receipts,
            }
        )
        if delay_seconds is not UNSET:
            field_dict["delay_seconds"] = delay_seconds
        if error is not UNSET:
            field_dict["error"] = error
        if visibility_timeout_seconds is not UNSET:
            field_dict["visibility_timeout_seconds"] = visibility_timeout_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        receipts = cast(list[str], d.pop("receipts"))

        delay_seconds = d.pop("delay_seconds", UNSET)

        error = d.pop("error", UNSET)

        visibility_timeout_seconds = d.pop("visibility_timeout_seconds", UNSET)

        queue_lease_receipts_request = cls(
            receipts=receipts,
            delay_seconds=delay_seconds,
            error=error,
            visibility_timeout_seconds=visibility_timeout_seconds,
        )

        queue_lease_receipts_request.additional_properties = d
        return queue_lease_receipts_request

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
