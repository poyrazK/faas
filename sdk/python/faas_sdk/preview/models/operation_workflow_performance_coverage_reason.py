from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_performance_coverage_reason_reason import (
    OperationWorkflowPerformanceCoverageReasonReason,
    check_operation_workflow_performance_coverage_reason_reason,
)

T = TypeVar("T", bound="OperationWorkflowPerformanceCoverageReason")


@_attrs_define
class OperationWorkflowPerformanceCoverageReason:
    """Count of sampled workflows excluded for one retained-history coverage reason."""

    reason: OperationWorkflowPerformanceCoverageReasonReason
    workflow_count: int

    def to_dict(self) -> dict[str, Any]:
        reason: str = self.reason

        workflow_count = self.workflow_count

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "reason": reason,
                "workflow_count": workflow_count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        reason = check_operation_workflow_performance_coverage_reason_reason(d.pop("reason"))

        workflow_count = d.pop("workflow_count")

        operation_workflow_performance_coverage_reason = cls(
            reason=reason,
            workflow_count=workflow_count,
        )

        return operation_workflow_performance_coverage_reason
