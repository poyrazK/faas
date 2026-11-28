from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_build_job_inventory_source import (
    ObsRuntimeBuildJobInventorySource,
    check_obs_runtime_build_job_inventory_source,
)
from ..models.obs_runtime_build_job_state import ObsRuntimeBuildJobState, check_obs_runtime_build_job_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsRuntimeBuildJob")


@_attrs_define
class ObsRuntimeBuildJob:
    """Independent selected-job inventory and source availability."""

    job: str
    daemon: str
    inventory_source: ObsRuntimeBuildJobInventorySource
    state: ObsRuntimeBuildJobState
    expected: int
    reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        job = self.job

        daemon = self.daemon

        inventory_source: str = self.inventory_source

        state: str = self.state

        expected = self.expected

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "job": job,
                "daemon": daemon,
                "inventory_source": inventory_source,
                "state": state,
                "expected": expected,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        job = d.pop("job")

        daemon = d.pop("daemon")

        inventory_source = check_obs_runtime_build_job_inventory_source(d.pop("inventory_source"))

        state = check_obs_runtime_build_job_state(d.pop("state"))

        expected = d.pop("expected")

        reason = d.pop("reason", UNSET)

        obs_runtime_build_job = cls(
            job=job,
            daemon=daemon,
            inventory_source=inventory_source,
            state=state,
            expected=expected,
            reason=reason,
        )

        obs_runtime_build_job.additional_properties = d
        return obs_runtime_build_job

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
