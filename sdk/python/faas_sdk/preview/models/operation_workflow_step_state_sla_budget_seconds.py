from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="OperationWorkflowStepStateSlaBudgetSeconds")


@_attrs_define
class OperationWorkflowStepStateSlaBudgetSeconds:
    """Optional observed state visit budgets. Keys must be declared nonterminal states. All workflow steps in one
    definition must agree; publish a new workflow contract version for budget changes. Metadata updates do not reset a
    visit clock. Unknown retained entry times do not imply a breach.

    """

    additional_properties: dict[str, int] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        operation_workflow_step_state_sla_budget_seconds = cls()

        operation_workflow_step_state_sla_budget_seconds.additional_properties = d
        return operation_workflow_step_state_sla_budget_seconds

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> int:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: int) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
