from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ScenarioManagedWorkBinding")


@_attrs_define
class ScenarioManagedWorkBinding:
    """Platform identities and fixture resources bound to a logical workload. Missing identities make evidence incomplete."""

    workload: str
    app_id: UUID
    deployment_id: str
    postgres_resource_id: UUID | Unset = UNSET
    objects_resource_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        workload = self.workload

        app_id = str(self.app_id)

        deployment_id = self.deployment_id

        postgres_resource_id: str | Unset = UNSET
        if not isinstance(self.postgres_resource_id, Unset):
            postgres_resource_id = str(self.postgres_resource_id)

        objects_resource_id: str | Unset = UNSET
        if not isinstance(self.objects_resource_id, Unset):
            objects_resource_id = str(self.objects_resource_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "workload": workload,
                "app_id": app_id,
                "deployment_id": deployment_id,
            }
        )
        if postgres_resource_id is not UNSET:
            field_dict["postgres_resource_id"] = postgres_resource_id
        if objects_resource_id is not UNSET:
            field_dict["objects_resource_id"] = objects_resource_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        workload = d.pop("workload")

        app_id = UUID(d.pop("app_id"))

        deployment_id = d.pop("deployment_id")

        _postgres_resource_id = d.pop("postgres_resource_id", UNSET)
        postgres_resource_id: UUID | Unset
        if isinstance(_postgres_resource_id, Unset):
            postgres_resource_id = UNSET
        else:
            postgres_resource_id = UUID(_postgres_resource_id)

        _objects_resource_id = d.pop("objects_resource_id", UNSET)
        objects_resource_id: UUID | Unset
        if isinstance(_objects_resource_id, Unset):
            objects_resource_id = UNSET
        else:
            objects_resource_id = UUID(_objects_resource_id)

        scenario_managed_work_binding = cls(
            workload=workload,
            app_id=app_id,
            deployment_id=deployment_id,
            postgres_resource_id=postgres_resource_id,
            objects_resource_id=objects_resource_id,
        )

        scenario_managed_work_binding.additional_properties = d
        return scenario_managed_work_binding

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
