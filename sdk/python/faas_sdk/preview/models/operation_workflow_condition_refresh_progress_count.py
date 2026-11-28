from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_condition_refresh_progress_count_status import (
    OperationWorkflowConditionRefreshProgressCountStatus,
    check_operation_workflow_condition_refresh_progress_count_status,
)

T = TypeVar("T", bound="OperationWorkflowConditionRefreshProgressCount")


@_attrs_define
class OperationWorkflowConditionRefreshProgressCount:
    """Exact condition count for one reporting progress status, computed before preview truncation. Refreshed means a newer
    report within its pinned age budget; still_overdue includes newer reports already overdue. Unreported denotes an
    absent exact condition and decision/rule identity, not proof of a cleared business condition. Contract changes are
    not compared across definitions. Budget removal is not a refresh.

    """

    status: OperationWorkflowConditionRefreshProgressCountStatus
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
        status = check_operation_workflow_condition_refresh_progress_count_status(d.pop("status"))

        condition_count = d.pop("condition_count")

        operation_workflow_condition_refresh_progress_count = cls(
            status=status,
            condition_count=condition_count,
        )

        return operation_workflow_condition_refresh_progress_count
