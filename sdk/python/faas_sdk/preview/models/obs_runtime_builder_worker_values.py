from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsRuntimeBuilderWorkerValues")


@_attrs_define
class ObsRuntimeBuilderWorkerValues:
    """Configured poll and in-flight allowances with the existing 60-second scrape/evaluation margin. Completed iterations
    include idle/error returns. Initialization is the origin before any completion. Intentional drain never establishes
    resumed work.

    """

    enabled: bool
    inflight: bool
    started_at: datetime.datetime
    interval_seconds: float
    busy_allowance_seconds: float
    progress_age_seconds: float
    progress_allowance_seconds: float
    last_attempt_at: datetime.datetime | Unset = UNSET
    last_completed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        inflight = self.inflight

        started_at = self.started_at.isoformat()

        interval_seconds = self.interval_seconds

        busy_allowance_seconds = self.busy_allowance_seconds

        progress_age_seconds = self.progress_age_seconds

        progress_allowance_seconds = self.progress_allowance_seconds

        last_attempt_at: str | Unset = UNSET
        if not isinstance(self.last_attempt_at, Unset):
            last_attempt_at = self.last_attempt_at.isoformat()

        last_completed_at: str | Unset = UNSET
        if not isinstance(self.last_completed_at, Unset):
            last_completed_at = self.last_completed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
                "inflight": inflight,
                "started_at": started_at,
                "interval_seconds": interval_seconds,
                "busy_allowance_seconds": busy_allowance_seconds,
                "progress_age_seconds": progress_age_seconds,
                "progress_allowance_seconds": progress_allowance_seconds,
            }
        )
        if last_attempt_at is not UNSET:
            field_dict["last_attempt_at"] = last_attempt_at
        if last_completed_at is not UNSET:
            field_dict["last_completed_at"] = last_completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        inflight = d.pop("inflight")

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        interval_seconds = d.pop("interval_seconds")

        busy_allowance_seconds = d.pop("busy_allowance_seconds")

        progress_age_seconds = d.pop("progress_age_seconds")

        progress_allowance_seconds = d.pop("progress_allowance_seconds")

        _last_attempt_at = d.pop("last_attempt_at", UNSET)
        last_attempt_at: datetime.datetime | Unset
        if isinstance(_last_attempt_at, Unset):
            last_attempt_at = UNSET
        else:
            last_attempt_at = datetime.datetime.fromisoformat(_last_attempt_at)

        _last_completed_at = d.pop("last_completed_at", UNSET)
        last_completed_at: datetime.datetime | Unset
        if isinstance(_last_completed_at, Unset):
            last_completed_at = UNSET
        else:
            last_completed_at = datetime.datetime.fromisoformat(_last_completed_at)

        obs_runtime_builder_worker_values = cls(
            enabled=enabled,
            inflight=inflight,
            started_at=started_at,
            interval_seconds=interval_seconds,
            busy_allowance_seconds=busy_allowance_seconds,
            progress_age_seconds=progress_age_seconds,
            progress_allowance_seconds=progress_allowance_seconds,
            last_attempt_at=last_attempt_at,
            last_completed_at=last_completed_at,
        )

        obs_runtime_builder_worker_values.additional_properties = d
        return obs_runtime_builder_worker_values

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
