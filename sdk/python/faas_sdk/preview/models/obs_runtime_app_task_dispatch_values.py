from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsRuntimeAppTaskDispatchValues")


@_attrs_define
class ObsRuntimeAppTaskDispatchValues:
    """Explicit local dispatch and running bits and configured/occupied handler slots. Last claim success includes empty
    results; last sweep success includes empty sweeps. Both clocks are absent before success. They are not task
    completion or delivery acknowledgements.

    """

    enabled: bool
    running: bool
    slots_configured: int
    slots_in_use: int
    last_claim_success_at: datetime.datetime | Unset = UNSET
    last_sweep_success_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        running = self.running

        slots_configured = self.slots_configured

        slots_in_use = self.slots_in_use

        last_claim_success_at: str | Unset = UNSET
        if not isinstance(self.last_claim_success_at, Unset):
            last_claim_success_at = self.last_claim_success_at.isoformat()

        last_sweep_success_at: str | Unset = UNSET
        if not isinstance(self.last_sweep_success_at, Unset):
            last_sweep_success_at = self.last_sweep_success_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
                "running": running,
                "slots_configured": slots_configured,
                "slots_in_use": slots_in_use,
            }
        )
        if last_claim_success_at is not UNSET:
            field_dict["last_claim_success_at"] = last_claim_success_at
        if last_sweep_success_at is not UNSET:
            field_dict["last_sweep_success_at"] = last_sweep_success_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        running = d.pop("running")

        slots_configured = d.pop("slots_configured")

        slots_in_use = d.pop("slots_in_use")

        _last_claim_success_at = d.pop("last_claim_success_at", UNSET)
        last_claim_success_at: datetime.datetime | Unset
        if isinstance(_last_claim_success_at, Unset):
            last_claim_success_at = UNSET
        else:
            last_claim_success_at = datetime.datetime.fromisoformat(_last_claim_success_at)

        _last_sweep_success_at = d.pop("last_sweep_success_at", UNSET)
        last_sweep_success_at: datetime.datetime | Unset
        if isinstance(_last_sweep_success_at, Unset):
            last_sweep_success_at = UNSET
        else:
            last_sweep_success_at = datetime.datetime.fromisoformat(_last_sweep_success_at)

        obs_runtime_app_task_dispatch_values = cls(
            enabled=enabled,
            running=running,
            slots_configured=slots_configured,
            slots_in_use=slots_in_use,
            last_claim_success_at=last_claim_success_at,
            last_sweep_success_at=last_sweep_success_at,
        )

        obs_runtime_app_task_dispatch_values.additional_properties = d
        return obs_runtime_app_task_dispatch_values

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
