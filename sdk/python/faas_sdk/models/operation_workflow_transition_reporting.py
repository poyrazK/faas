from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_transition_reporting_status import (
    OperationWorkflowTransitionReportingStatus,
    check_operation_workflow_transition_reporting_status,
)

if TYPE_CHECKING:
    from ..models.operation_workflow_condition_coverage_entry import OperationWorkflowConditionCoverageEntry


T = TypeVar("T", bound="OperationWorkflowTransitionReporting")


@_attrs_define
class OperationWorkflowTransitionReporting:
    """Workflow-wide current-state expected condition coverage evaluated from the latest reporting Operation pinned
    definition and retained explicit decision contexts. It does not add transition guards. Counts precede previews.
    Unknown coverage is never inferred as satisfied; source evidence uses its own pinned freshness budget. Conditions
    apply to current state rather than proposed destination state.

    """

    evaluated_at: datetime.datetime
    state: str
    reporting_operation_id: UUID
    status: OperationWorkflowTransitionReportingStatus
    expected_condition_count: int
    reported_condition_count: int
    missing_condition_count: int
    overdue_condition_count: int
    conditions: list[OperationWorkflowConditionCoverageEntry]
    conditions_truncated: bool

    def to_dict(self) -> dict[str, Any]:
        evaluated_at = self.evaluated_at.isoformat()

        state = self.state

        reporting_operation_id = str(self.reporting_operation_id)

        status: str = self.status

        expected_condition_count = self.expected_condition_count

        reported_condition_count = self.reported_condition_count

        missing_condition_count = self.missing_condition_count

        overdue_condition_count = self.overdue_condition_count

        conditions = []
        for conditions_item_data in self.conditions:
            conditions_item = conditions_item_data.to_dict()
            conditions.append(conditions_item)

        conditions_truncated = self.conditions_truncated

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "evaluated_at": evaluated_at,
                "state": state,
                "reporting_operation_id": reporting_operation_id,
                "status": status,
                "expected_condition_count": expected_condition_count,
                "reported_condition_count": reported_condition_count,
                "missing_condition_count": missing_condition_count,
                "overdue_condition_count": overdue_condition_count,
                "conditions": conditions,
                "conditions_truncated": conditions_truncated,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_condition_coverage_entry import OperationWorkflowConditionCoverageEntry

        d = dict(src_dict)
        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        state = d.pop("state")

        reporting_operation_id = UUID(d.pop("reporting_operation_id"))

        status = check_operation_workflow_transition_reporting_status(d.pop("status"))

        expected_condition_count = d.pop("expected_condition_count")

        reported_condition_count = d.pop("reported_condition_count")

        missing_condition_count = d.pop("missing_condition_count")

        overdue_condition_count = d.pop("overdue_condition_count")

        conditions = []
        _conditions = d.pop("conditions")
        for conditions_item_data in _conditions:
            conditions_item = OperationWorkflowConditionCoverageEntry.from_dict(conditions_item_data)

            conditions.append(conditions_item)

        conditions_truncated = d.pop("conditions_truncated")

        operation_workflow_transition_reporting = cls(
            evaluated_at=evaluated_at,
            state=state,
            reporting_operation_id=reporting_operation_id,
            status=status,
            expected_condition_count=expected_condition_count,
            reported_condition_count=reported_condition_count,
            missing_condition_count=missing_condition_count,
            overdue_condition_count=overdue_condition_count,
            conditions=conditions,
            conditions_truncated=conditions_truncated,
        )

        return operation_workflow_transition_reporting
