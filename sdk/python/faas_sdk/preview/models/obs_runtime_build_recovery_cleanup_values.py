from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsRuntimeBuildRecoveryCleanupValues")


@_attrs_define
class ObsRuntimeBuildRecoveryCleanupValues:
    """Pending durable obligations include claimed and retrying rows. Retried is the subset with a recorded failed attempt,
    including later live claims; it is not an attempt count or retry rate. Age starts at the oldest obligation creation
    and survives retries. Positive pending requires oldest creation and age; empty sets omit both timing fields.

    """

    pending: int
    retried: int
    oldest_created_at: datetime.datetime | Unset = UNSET
    oldest_age_seconds: float | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        pending = self.pending

        retried = self.retried

        oldest_created_at: str | Unset = UNSET
        if not isinstance(self.oldest_created_at, Unset):
            oldest_created_at = self.oldest_created_at.isoformat()

        oldest_age_seconds = self.oldest_age_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "pending": pending,
                "retried": retried,
            }
        )
        if oldest_created_at is not UNSET:
            field_dict["oldest_created_at"] = oldest_created_at
        if oldest_age_seconds is not UNSET:
            field_dict["oldest_age_seconds"] = oldest_age_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        pending = d.pop("pending")

        retried = d.pop("retried")

        _oldest_created_at = d.pop("oldest_created_at", UNSET)
        oldest_created_at: datetime.datetime | Unset
        if isinstance(_oldest_created_at, Unset):
            oldest_created_at = UNSET
        else:
            oldest_created_at = datetime.datetime.fromisoformat(_oldest_created_at)

        oldest_age_seconds = d.pop("oldest_age_seconds", UNSET)

        obs_runtime_build_recovery_cleanup_values = cls(
            pending=pending,
            retried=retried,
            oldest_created_at=oldest_created_at,
            oldest_age_seconds=oldest_age_seconds,
        )

        obs_runtime_build_recovery_cleanup_values.additional_properties = d
        return obs_runtime_build_recovery_cleanup_values

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
