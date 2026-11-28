from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scenario_managed_work_plan_version import (
    ScenarioManagedWorkPlanVersion,
    check_scenario_managed_work_plan_version,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.scenario_managed_work_participant import ScenarioManagedWorkParticipant
    from ..models.scenario_postgres_fault_spec import ScenarioPostgresFaultSpec
    from ..models.scenario_worker_termination import ScenarioWorkerTermination


T = TypeVar("T", bound="ScenarioManagedWorkPlan")


@_attrs_define
class ScenarioManagedWorkPlan:
    """Explicit participants and fixture permissions for managed async-route rehearsals. Requires contained commands and
    simulation; rollout is operator gated.

    """

    version: ScenarioManagedWorkPlanVersion
    primary: str
    participants: list[ScenarioManagedWorkParticipant]
    worker_termination: ScenarioWorkerTermination | Unset = UNSET
    """One cooperative worker termination at a named POST HTTP simulation checkpoint. The target must execute one
    job and recover on a fresh VM; rollout is separately operator gated."""
    postgres_fault: ScenarioPostgresFaultSpec | Unset = UNSET
    """Separately gated warm rehearsal with one managed worker and one isolated database. Disconnects existing
    sessions and rejects new connections before restoring access. Excludes cold start, suspend/resume and worker
    termination."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        primary = self.primary

        participants = []
        for participants_item_data in self.participants:
            participants_item = participants_item_data.to_dict()
            participants.append(participants_item)

        worker_termination: dict[str, Any] | Unset = UNSET
        if not isinstance(self.worker_termination, Unset):
            worker_termination = self.worker_termination.to_dict()

        postgres_fault: dict[str, Any] | Unset = UNSET
        if not isinstance(self.postgres_fault, Unset):
            postgres_fault = self.postgres_fault.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "primary": primary,
                "participants": participants,
            }
        )
        if worker_termination is not UNSET:
            field_dict["worker_termination"] = worker_termination
        if postgres_fault is not UNSET:
            field_dict["postgres_fault"] = postgres_fault

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.scenario_managed_work_participant import ScenarioManagedWorkParticipant
        from ..models.scenario_postgres_fault_spec import ScenarioPostgresFaultSpec
        from ..models.scenario_worker_termination import ScenarioWorkerTermination

        d = dict(src_dict)
        version = check_scenario_managed_work_plan_version(d.pop("version"))

        primary = d.pop("primary")

        participants = []
        _participants = d.pop("participants")
        for participants_item_data in _participants:
            participants_item = ScenarioManagedWorkParticipant.from_dict(participants_item_data)

            participants.append(participants_item)

        _worker_termination = d.pop("worker_termination", UNSET)
        worker_termination: ScenarioWorkerTermination | Unset
        if isinstance(_worker_termination, Unset):
            worker_termination = UNSET
        else:
            worker_termination = ScenarioWorkerTermination.from_dict(_worker_termination)

        _postgres_fault = d.pop("postgres_fault", UNSET)
        postgres_fault: ScenarioPostgresFaultSpec | Unset
        if isinstance(_postgres_fault, Unset):
            postgres_fault = UNSET
        else:
            postgres_fault = ScenarioPostgresFaultSpec.from_dict(_postgres_fault)

        scenario_managed_work_plan = cls(
            version=version,
            primary=primary,
            participants=participants,
            worker_termination=worker_termination,
            postgres_fault=postgres_fault,
        )

        scenario_managed_work_plan.additional_properties = d
        return scenario_managed_work_plan

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
