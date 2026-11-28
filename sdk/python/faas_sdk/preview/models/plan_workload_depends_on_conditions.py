from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.plan_workload_depends_on_conditions_additional_property import (
    PlanWorkloadDependsOnConditionsAdditionalProperty,
    check_plan_workload_depends_on_conditions_additional_property,
)

T = TypeVar("T", bound="PlanWorkloadDependsOnConditions")


@_attrs_define
class PlanWorkloadDependsOnConditions:
    """Explicit Compose dependency conditions. service_started retains admission ordering; service_healthy gates release on
    the captured same-project, same-environment dependency deployment.

    """

    additional_properties: dict[str, PlanWorkloadDependsOnConditionsAdditionalProperty] = _attrs_field(
        init=False, factory=dict
    )

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        for prop_name, prop in self.additional_properties.items():
            field_dict[prop_name] = prop

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        plan_workload_depends_on_conditions = cls()

        additional_properties = {}
        for prop_name, prop_dict in d.items():
            additional_property = check_plan_workload_depends_on_conditions_additional_property(prop_dict)

            additional_properties[prop_name] = additional_property

        plan_workload_depends_on_conditions.additional_properties = additional_properties
        return plan_workload_depends_on_conditions

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> PlanWorkloadDependsOnConditionsAdditionalProperty:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: PlanWorkloadDependsOnConditionsAdditionalProperty) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
