from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsRuntimeBuildRecoveryRunningValues")


@_attrs_define
class ObsRuntimeBuildRecoveryRunningValues:
    """Current running claims in the shared fleet view. Positive counts require oldest start and age; empty sets omit both
    timing fields. Claim age may precede the rollout and does not establish current VM activity or a new stall deadline.
    Count ceiling is exact-integer wire safety, not an admission quota.

    """

    count: int
    oldest_started_at: datetime.datetime | Unset = UNSET
    oldest_age_seconds: float | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        count = self.count

        oldest_started_at: str | Unset = UNSET
        if not isinstance(self.oldest_started_at, Unset):
            oldest_started_at = self.oldest_started_at.isoformat()

        oldest_age_seconds = self.oldest_age_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "count": count,
            }
        )
        if oldest_started_at is not UNSET:
            field_dict["oldest_started_at"] = oldest_started_at
        if oldest_age_seconds is not UNSET:
            field_dict["oldest_age_seconds"] = oldest_age_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        count = d.pop("count")

        _oldest_started_at = d.pop("oldest_started_at", UNSET)
        oldest_started_at: datetime.datetime | Unset
        if isinstance(_oldest_started_at, Unset):
            oldest_started_at = UNSET
        else:
            oldest_started_at = datetime.datetime.fromisoformat(_oldest_started_at)

        oldest_age_seconds = d.pop("oldest_age_seconds", UNSET)

        obs_runtime_build_recovery_running_values = cls(
            count=count,
            oldest_started_at=oldest_started_at,
            oldest_age_seconds=oldest_age_seconds,
        )

        obs_runtime_build_recovery_running_values.additional_properties = d
        return obs_runtime_build_recovery_running_values

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
