from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_condition_comparison_status import (
    OperationWorkflowConditionComparisonStatus,
    check_operation_workflow_condition_comparison_status,
)

if TYPE_CHECKING:
    from ..models.operation_workflow_condition_change import OperationWorkflowConditionChange


T = TypeVar("T", bound="OperationWorkflowConditionComparison")


@_attrs_define
class OperationWorkflowConditionComparison:
    """Condition changes in explicit decision snapshots between consecutive retained revisions in the same tenant and
    workflow run and pinned contract. Shares history comparison statuses; missing decisions on metadata reports do not
    clear conditions. No causal inference.

    """

    status: OperationWorkflowConditionComparisonStatus
    changes: list[OperationWorkflowConditionChange]
    """Reason conditions first then path groups then condition IDs in lexical order. Unchanged conditions are
    omitted. Contributor evidence carries changes for the entire qualifying report."""
    truncated: bool
    """More condition changes exist than the 64 retained entries."""

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        changes = []
        for changes_item_data in self.changes:
            changes_item = changes_item_data.to_dict()
            changes.append(changes_item)

        truncated = self.truncated

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "status": status,
                "changes": changes,
                "truncated": truncated,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_condition_change import OperationWorkflowConditionChange

        d = dict(src_dict)
        status = check_operation_workflow_condition_comparison_status(d.pop("status"))

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = OperationWorkflowConditionChange.from_dict(changes_item_data)

            changes.append(changes_item)

        truncated = d.pop("truncated")

        operation_workflow_condition_comparison = cls(
            status=status,
            changes=changes,
            truncated=truncated,
        )

        return operation_workflow_condition_comparison
