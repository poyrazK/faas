from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_new_missing_condition_progress_count_status import (
    OperationWorkflowNewMissingConditionProgressCountStatus,
    check_operation_workflow_new_missing_condition_progress_count_status,
)

T = TypeVar("T", bound="OperationWorkflowNewMissingConditionProgressCount")


@_attrs_define
class OperationWorkflowNewMissingConditionProgressCount:
    """Exact count of current missing conditions by comparison category before previews. Still_missing is an existing gap,
    while report_lost requires the identical baseline expectation and prior evidence. New or changed expectations and
    contract changes are distinct.

    """

    status: OperationWorkflowNewMissingConditionProgressCountStatus
    condition_count: int

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        condition_count = self.condition_count

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "status": status,
                "condition_count": condition_count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_operation_workflow_new_missing_condition_progress_count_status(d.pop("status"))

        condition_count = d.pop("condition_count")

        operation_workflow_new_missing_condition_progress_count = cls(
            status=status,
            condition_count=condition_count,
        )

        return operation_workflow_new_missing_condition_progress_count
