from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_gap_queue_guidance_status import (
    OperationWorkflowGapQueueGuidanceStatus,
    check_operation_workflow_gap_queue_guidance_status,
)

if TYPE_CHECKING:
    from ..models.operation_workflow_condition_coverage_entry import OperationWorkflowConditionCoverageEntry


T = TypeVar("T", bound="OperationWorkflowGapQueueGuidance")


@_attrs_define
class OperationWorkflowGapQueueGuidance:
    """Pinned public expectation and comparison category for one current reporting gap, without prior evidence facts."""

    status: OperationWorkflowGapQueueGuidanceStatus
    expectation: OperationWorkflowConditionCoverageEntry
    """One condition expected in the current state. Match the declaring workflow step and exact condition ID and
    description within the current contract and latest explicit contexts. Evidence keeps the source report pinned
    age budget and decision identity. Reported includes any application result and may have no freshness budget;
    overdue is evidence past its budget. Missing has no evidence. This grants no authorization or verification."""

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        expectation = self.expectation.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "status": status,
                "expectation": expectation,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_condition_coverage_entry import OperationWorkflowConditionCoverageEntry

        d = dict(src_dict)
        status = check_operation_workflow_gap_queue_guidance_status(d.pop("status"))

        expectation = OperationWorkflowConditionCoverageEntry.from_dict(d.pop("expectation"))

        operation_workflow_gap_queue_guidance = cls(
            status=status,
            expectation=expectation,
        )

        return operation_workflow_gap_queue_guidance
