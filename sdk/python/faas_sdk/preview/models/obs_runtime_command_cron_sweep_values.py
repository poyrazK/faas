from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsRuntimeCommandCronSweepValues")


@_attrs_define
class ObsRuntimeCommandCronSweepValues:
    """Explicit scan-in-progress bit, last actual scan start and last scan that completed listing and owner lookups
    successfully, including empty scans. Start and success are absent before observation. Running requires an observed
    start. An idle scan does not imply success or disabled cron intent; no new progress deadline is inferred.

    """

    running: bool
    last_started_at: datetime.datetime | Unset = UNSET
    last_success_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        running = self.running

        last_started_at: str | Unset = UNSET
        if not isinstance(self.last_started_at, Unset):
            last_started_at = self.last_started_at.isoformat()

        last_success_at: str | Unset = UNSET
        if not isinstance(self.last_success_at, Unset):
            last_success_at = self.last_success_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "running": running,
            }
        )
        if last_started_at is not UNSET:
            field_dict["last_started_at"] = last_started_at
        if last_success_at is not UNSET:
            field_dict["last_success_at"] = last_success_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        running = d.pop("running")

        _last_started_at = d.pop("last_started_at", UNSET)
        last_started_at: datetime.datetime | Unset
        if isinstance(_last_started_at, Unset):
            last_started_at = UNSET
        else:
            last_started_at = datetime.datetime.fromisoformat(_last_started_at)

        _last_success_at = d.pop("last_success_at", UNSET)
        last_success_at: datetime.datetime | Unset
        if isinstance(_last_success_at, Unset):
            last_success_at = UNSET
        else:
            last_success_at = datetime.datetime.fromisoformat(_last_success_at)

        obs_runtime_command_cron_sweep_values = cls(
            running=running,
            last_started_at=last_started_at,
            last_success_at=last_success_at,
        )

        obs_runtime_command_cron_sweep_values.additional_properties = d
        return obs_runtime_command_cron_sweep_values

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
