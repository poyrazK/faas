from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_execution import OperationExecution


T = TypeVar("T", bound="OperationExecutionsResponse")


@_attrs_define
class OperationExecutionsResponse:
    """Ascending bounded page of retained execution generations."""

    executions: list[OperationExecution]
    next_generation: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        executions = []
        for executions_item_data in self.executions:
            executions_item = executions_item_data.to_dict()
            executions.append(executions_item)

        next_generation = self.next_generation

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "executions": executions,
            }
        )
        if next_generation is not UNSET:
            field_dict["next_generation"] = next_generation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_execution import OperationExecution

        d = dict(src_dict)
        executions = []
        _executions = d.pop("executions")
        for executions_item_data in _executions:
            executions_item = OperationExecution.from_dict(executions_item_data)

            executions.append(executions_item)

        next_generation = d.pop("next_generation", UNSET)

        operation_executions_response = cls(
            executions=executions,
            next_generation=next_generation,
        )

        operation_executions_response.additional_properties = d
        return operation_executions_response

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
