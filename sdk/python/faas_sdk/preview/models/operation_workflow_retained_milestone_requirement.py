from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowRetainedMilestoneRequirement")


@_attrs_define
class OperationWorkflowRetainedMilestoneRequirement:
    """Source operation and milestone bound to a declared transaction-backed step of the selected workflow contract."""

    operation: str
    milestone: str

    def to_dict(self) -> dict[str, Any]:
        operation = self.operation

        milestone = self.milestone

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "operation": operation,
                "milestone": milestone,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        operation = d.pop("operation")

        milestone = d.pop("milestone")

        operation_workflow_retained_milestone_requirement = cls(
            operation=operation,
            milestone=milestone,
        )

        return operation_workflow_retained_milestone_requirement
