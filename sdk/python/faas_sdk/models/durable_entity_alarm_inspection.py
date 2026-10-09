from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DurableEntityAlarmInspection")


@_attrs_define
class DurableEntityAlarmInspection:
    attempts: int
    exhausted: bool
    alarm_at: datetime.datetime | Unset = UNSET
    next_attempt_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        attempts = self.attempts

        exhausted = self.exhausted

        alarm_at: str | Unset = UNSET
        if not isinstance(self.alarm_at, Unset):
            alarm_at = self.alarm_at.isoformat()

        next_attempt_at: str | Unset = UNSET
        if not isinstance(self.next_attempt_at, Unset):
            next_attempt_at = self.next_attempt_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "attempts": attempts,
                "exhausted": exhausted,
            }
        )
        if alarm_at is not UNSET:
            field_dict["alarm_at"] = alarm_at
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        attempts = d.pop("attempts")

        exhausted = d.pop("exhausted")

        _alarm_at = d.pop("alarm_at", UNSET)
        alarm_at: datetime.datetime | Unset
        if isinstance(_alarm_at, Unset):
            alarm_at = UNSET
        else:
            alarm_at = datetime.datetime.fromisoformat(_alarm_at)

        _next_attempt_at = d.pop("next_attempt_at", UNSET)
        next_attempt_at: datetime.datetime | Unset
        if isinstance(_next_attempt_at, Unset):
            next_attempt_at = UNSET
        else:
            next_attempt_at = datetime.datetime.fromisoformat(_next_attempt_at)

        durable_entity_alarm_inspection = cls(
            attempts=attempts,
            exhausted=exhausted,
            alarm_at=alarm_at,
            next_attempt_at=next_attempt_at,
        )

        durable_entity_alarm_inspection.additional_properties = d
        return durable_entity_alarm_inspection

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
