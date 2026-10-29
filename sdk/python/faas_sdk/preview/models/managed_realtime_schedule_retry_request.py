from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedRealtimeScheduleRetryRequest")


@_attrs_define
class ManagedRealtimeScheduleRetryRequest:
    """Version-checked manual retry with an optional new delivery time."""

    expected_version: int
    deliver_at: datetime.datetime | Unset = UNSET
    """Optional future retry time within 30 days; omit to retry on the next worker pass."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        deliver_at: str | Unset = UNSET
        if not isinstance(self.deliver_at, Unset):
            deliver_at = self.deliver_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "expected_version": expected_version,
            }
        )
        if deliver_at is not UNSET:
            field_dict["deliver_at"] = deliver_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        _deliver_at = d.pop("deliver_at", UNSET)
        deliver_at: datetime.datetime | Unset
        if isinstance(_deliver_at, Unset):
            deliver_at = UNSET
        else:
            deliver_at = datetime.datetime.fromisoformat(_deliver_at)

        managed_realtime_schedule_retry_request = cls(
            expected_version=expected_version,
            deliver_at=deliver_at,
        )

        managed_realtime_schedule_retry_request.additional_properties = d
        return managed_realtime_schedule_retry_request

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
