from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scenario_managed_work_participant_objects import (
    ScenarioManagedWorkParticipantObjects,
    check_scenario_managed_work_participant_objects,
)
from ..models.scenario_managed_work_participant_postgres import (
    ScenarioManagedWorkParticipantPostgres,
    check_scenario_managed_work_participant_postgres,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ScenarioManagedWorkParticipant")


@_attrs_define
class ScenarioManagedWorkParticipant:
    """One enrolled workload and its explicit resource capabilities. Async workers must execute at least one managed job."""

    workload: str
    async_: bool | Unset = UNSET
    postgres: ScenarioManagedWorkParticipantPostgres | Unset = UNSET
    objects: ScenarioManagedWorkParticipantObjects | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        workload = self.workload

        async_ = self.async_

        postgres: str | Unset = UNSET
        if not isinstance(self.postgres, Unset):
            postgres = self.postgres

        objects: str | Unset = UNSET
        if not isinstance(self.objects, Unset):
            objects = self.objects

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "workload": workload,
            }
        )
        if async_ is not UNSET:
            field_dict["async"] = async_
        if postgres is not UNSET:
            field_dict["postgres"] = postgres
        if objects is not UNSET:
            field_dict["objects"] = objects

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        workload = d.pop("workload")

        async_ = d.pop("async", UNSET)

        _postgres = d.pop("postgres", UNSET)
        postgres: ScenarioManagedWorkParticipantPostgres | Unset
        if isinstance(_postgres, Unset):
            postgres = UNSET
        else:
            postgres = check_scenario_managed_work_participant_postgres(_postgres)

        _objects = d.pop("objects", UNSET)
        objects: ScenarioManagedWorkParticipantObjects | Unset
        if isinstance(_objects, Unset):
            objects = UNSET
        else:
            objects = check_scenario_managed_work_participant_objects(_objects)

        scenario_managed_work_participant = cls(
            workload=workload,
            async_=async_,
            postgres=postgres,
            objects=objects,
        )

        scenario_managed_work_participant.additional_properties = d
        return scenario_managed_work_participant

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
