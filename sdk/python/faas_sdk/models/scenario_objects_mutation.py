from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scenario_objects_mutation_method import (
    ScenarioObjectsMutationMethod,
    check_scenario_objects_mutation_method,
)
from ..models.scenario_objects_mutation_status import (
    ScenarioObjectsMutationStatus,
    check_scenario_objects_mutation_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.scenario_objects_object import ScenarioObjectsObject


T = TypeVar("T", bound="ScenarioObjectsMutation")


@_attrs_define
class ScenarioObjectsMutation:
    """Successful mutation completion receipt; repeated writes remain separate entries."""

    sequence: int
    """Mutation completion order within the namespace."""
    method: ScenarioObjectsMutationMethod
    key: str
    status: ScenarioObjectsMutationStatus
    workload: str | Unset = UNSET
    """Logical participant responsible for this mutation when managed work is enrolled."""
    object_: ScenarioObjectsObject | Unset = UNSET
    """Independent key, byte length, content digest and stable metadata without object content."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sequence = self.sequence

        method: str = self.method

        key = self.key

        status: int = self.status

        workload = self.workload

        object_: dict[str, Any] | Unset = UNSET
        if not isinstance(self.object_, Unset):
            object_ = self.object_.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sequence": sequence,
                "method": method,
                "key": key,
                "status": status,
            }
        )
        if workload is not UNSET:
            field_dict["workload"] = workload
        if object_ is not UNSET:
            field_dict["object"] = object_

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.scenario_objects_object import ScenarioObjectsObject

        d = dict(src_dict)
        sequence = d.pop("sequence")

        method = check_scenario_objects_mutation_method(d.pop("method"))

        key = d.pop("key")

        status = check_scenario_objects_mutation_status(d.pop("status"))

        workload = d.pop("workload", UNSET)

        _object_ = d.pop("object", UNSET)
        object_: ScenarioObjectsObject | Unset
        if isinstance(_object_, Unset):
            object_ = UNSET
        else:
            object_ = ScenarioObjectsObject.from_dict(_object_)

        scenario_objects_mutation = cls(
            sequence=sequence,
            method=method,
            key=key,
            status=status,
            workload=workload,
            object_=object_,
        )

        scenario_objects_mutation.additional_properties = d
        return scenario_objects_mutation

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
