from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scenario_suspend_resume_spec_checkpoint import (
    ScenarioSuspendResumeSpecCheckpoint,
    check_scenario_suspend_resume_spec_checkpoint,
)
from ..models.scenario_suspend_resume_spec_version import (
    ScenarioSuspendResumeSpecVersion,
    check_scenario_suspend_resume_spec_version,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ScenarioSuspendResumeSpec")


@_attrs_define
class ScenarioSuspendResumeSpec:
    """One contained in-place pause after setup or at a cooperative managed-job checkpoint. No persistent VM snapshot is
    written.

    """

    version: ScenarioSuspendResumeSpecVersion
    checkpoint: ScenarioSuspendResumeSpecCheckpoint
    workloads: list[str]
    pause_ms: int
    within_ms: int
    """Maximum host-observed resume duration through hook and readiness."""
    worker_checkpoint: str | Unset = UNSET
    """Required for managed_job; names one successful POST HTTP simulation fixture on the sole selected async
    worker."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        checkpoint: str = self.checkpoint

        workloads = self.workloads

        pause_ms = self.pause_ms

        within_ms = self.within_ms

        worker_checkpoint = self.worker_checkpoint

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "checkpoint": checkpoint,
                "workloads": workloads,
                "pause_ms": pause_ms,
                "within_ms": within_ms,
            }
        )
        if worker_checkpoint is not UNSET:
            field_dict["worker_checkpoint"] = worker_checkpoint

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = check_scenario_suspend_resume_spec_version(d.pop("version"))

        checkpoint = check_scenario_suspend_resume_spec_checkpoint(d.pop("checkpoint"))

        workloads = cast(list[str], d.pop("workloads"))

        pause_ms = d.pop("pause_ms")

        within_ms = d.pop("within_ms")

        worker_checkpoint = d.pop("worker_checkpoint", UNSET)

        scenario_suspend_resume_spec = cls(
            version=version,
            checkpoint=checkpoint,
            workloads=workloads,
            pause_ms=pause_ms,
            within_ms=within_ms,
            worker_checkpoint=worker_checkpoint,
        )

        scenario_suspend_resume_spec.additional_properties = d
        return scenario_suspend_resume_spec

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
