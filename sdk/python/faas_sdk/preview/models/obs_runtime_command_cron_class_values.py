from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsRuntimeCommandCronClassValues")


@_attrs_define
class ObsRuntimeCommandCronClassValues:
    """Schedules in this ownership scope and included unknown-age subset. Known due timing is required only when schedules
    exceeds unknown_age; otherwise both timing fields are absent. Disabled, suspended, account-held, not-due and unknown
    classes have entirely unknown ages. Age starts at the first unconsumed nominal boundary using canonical timezone and
    DST rules; it is not continuous eligibility, command execution delay or a new stall threshold. Integer ceiling is
    wire safety, not an admission quota.

    """

    schedules: int
    unknown_age: int
    oldest_due_at: datetime.datetime | Unset = UNSET
    oldest_known_due_age_seconds: float | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        schedules = self.schedules

        unknown_age = self.unknown_age

        oldest_due_at: str | Unset = UNSET
        if not isinstance(self.oldest_due_at, Unset):
            oldest_due_at = self.oldest_due_at.isoformat()

        oldest_known_due_age_seconds = self.oldest_known_due_age_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "schedules": schedules,
                "unknown_age": unknown_age,
            }
        )
        if oldest_due_at is not UNSET:
            field_dict["oldest_due_at"] = oldest_due_at
        if oldest_known_due_age_seconds is not UNSET:
            field_dict["oldest_known_due_age_seconds"] = oldest_known_due_age_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        schedules = d.pop("schedules")

        unknown_age = d.pop("unknown_age")

        _oldest_due_at = d.pop("oldest_due_at", UNSET)
        oldest_due_at: datetime.datetime | Unset
        if isinstance(_oldest_due_at, Unset):
            oldest_due_at = UNSET
        else:
            oldest_due_at = datetime.datetime.fromisoformat(_oldest_due_at)

        oldest_known_due_age_seconds = d.pop("oldest_known_due_age_seconds", UNSET)

        obs_runtime_command_cron_class_values = cls(
            schedules=schedules,
            unknown_age=unknown_age,
            oldest_due_at=oldest_due_at,
            oldest_known_due_age_seconds=oldest_known_due_age_seconds,
        )

        obs_runtime_command_cron_class_values.additional_properties = d
        return obs_runtime_command_cron_class_values

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
