from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="OperationWorkflowStepStateSlaWarningPercent")


@_attrs_define
class OperationWorkflowStepStateSlaWarningPercent:
    """Optional whole-percent warning thresholds for states with SLA budgets. At the threshold a known current visit
    becomes at_risk until its budget is breached. Omitted states have no early warning. All steps must agree; publish a
    new workflow contract version when changing thresholds.

    """

    additional_properties: dict[str, int] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        operation_workflow_step_state_sla_warning_percent = cls()

        operation_workflow_step_state_sla_warning_percent.additional_properties = d
        return operation_workflow_step_state_sla_warning_percent

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
