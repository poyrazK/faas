from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workload_dependency_condition import WorkloadDependencyCondition, check_workload_dependency_condition
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkloadDependency")


@_attrs_define
class WorkloadDependency:
    """Dependency on another workload in the same deployment."""

    name: str
    """Workload name: main or another sidecar name."""
    condition: WorkloadDependencyCondition | Unset = "started"
    """Lifecycle condition required before the dependent workload starts."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        condition: str | Unset = UNSET
        if not isinstance(self.condition, Unset):
            condition = self.condition

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
            }
        )
        if condition is not UNSET:
            field_dict["condition"] = condition

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        _condition = d.pop("condition", UNSET)
        condition: WorkloadDependencyCondition | Unset
        if isinstance(_condition, Unset):
            condition = UNSET
        else:
            condition = check_workload_dependency_condition(_condition)

        workload_dependency = cls(
            name=name,
            condition=condition,
        )

        workload_dependency.additional_properties = d
        return workload_dependency

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
