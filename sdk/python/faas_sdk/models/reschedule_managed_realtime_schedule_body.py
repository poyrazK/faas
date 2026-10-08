from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RescheduleManagedRealtimeScheduleBody")


@_attrs_define
class RescheduleManagedRealtimeScheduleBody:
    deliver_at: datetime.datetime
    expected_version: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deliver_at = self.deliver_at.isoformat()

        expected_version = self.expected_version

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deliver_at": deliver_at,
                "expected_version": expected_version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deliver_at = datetime.datetime.fromisoformat(d.pop("deliver_at"))

        expected_version = d.pop("expected_version")

        reschedule_managed_realtime_schedule_body = cls(
            deliver_at=deliver_at,
            expected_version=expected_version,
        )

        reschedule_managed_realtime_schedule_body.additional_properties = d
        return reschedule_managed_realtime_schedule_body

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
