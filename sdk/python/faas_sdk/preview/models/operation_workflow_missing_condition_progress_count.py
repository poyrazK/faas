from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_missing_condition_progress_count_status import (
    OperationWorkflowMissingConditionProgressCountStatus,
    check_operation_workflow_missing_condition_progress_count_status,
)

T = TypeVar("T", bound="OperationWorkflowMissingConditionProgressCount")


@_attrs_define
class OperationWorkflowMissingConditionProgressCount:
    """Exact baseline missing condition count for one progress status before preview limits. Received reports may report
    fail or unknown or already be overdue; status does not mean verification passed.

    """

    status: OperationWorkflowMissingConditionProgressCountStatus
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
        status = check_operation_workflow_missing_condition_progress_count_status(d.pop("status"))

        condition_count = d.pop("condition_count")

        operation_workflow_missing_condition_progress_count = cls(
            status=status,
            condition_count=condition_count,
        )

        return operation_workflow_missing_condition_progress_count
