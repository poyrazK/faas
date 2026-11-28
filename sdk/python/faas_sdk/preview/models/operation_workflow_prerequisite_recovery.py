from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_prerequisite_recovery_reason import (
    OperationWorkflowPrerequisiteRecoveryReason,
    check_operation_workflow_prerequisite_recovery_reason,
)
from ..models.operation_workflow_prerequisite_recovery_steps_item import (
    OperationWorkflowPrerequisiteRecoveryStepsItem,
    check_operation_workflow_prerequisite_recovery_steps_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowPrerequisiteRecovery")


@_attrs_define
class OperationWorkflowPrerequisiteRecovery:
    """Application-owned steps to restore a missing or unusable retained prerequisite. Derived from the pinned selector;
    absent for verified evidence. This is guidance and does not establish that the producer operation is currently
    executable. Steps are ordered perform_operation then publish_milestone then recheck_readiness. Publication must use
    the same ownership boundary and workflow instance and contract version. Freshness limits still apply.

    """

    reason: OperationWorkflowPrerequisiteRecoveryReason
    operation: str
    milestone: str
    steps: list[OperationWorkflowPrerequisiteRecoveryStepsItem]
    after_occurred_at: datetime.datetime | Unset = UNSET
    """Exclusive occurrence threshold from the invalidating fact or expired prerequisite. Never fabricate an
    occurrence time to satisfy this threshold."""

    def to_dict(self) -> dict[str, Any]:
        reason: str = self.reason

        operation = self.operation

        milestone = self.milestone

        steps = []
        for steps_item_data in self.steps:
            steps_item: str = steps_item_data
            steps.append(steps_item)

        after_occurred_at: str | Unset = UNSET
        if not isinstance(self.after_occurred_at, Unset):
            after_occurred_at = self.after_occurred_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "reason": reason,
                "operation": operation,
                "milestone": milestone,
                "steps": steps,
            }
        )
        if after_occurred_at is not UNSET:
            field_dict["after_occurred_at"] = after_occurred_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        reason = check_operation_workflow_prerequisite_recovery_reason(d.pop("reason"))

        operation = d.pop("operation")

        milestone = d.pop("milestone")

        steps = []
        _steps = d.pop("steps")
        for steps_item_data in _steps:
            steps_item = check_operation_workflow_prerequisite_recovery_steps_item(steps_item_data)

            steps.append(steps_item)

        _after_occurred_at = d.pop("after_occurred_at", UNSET)
        after_occurred_at: datetime.datetime | Unset
        if isinstance(_after_occurred_at, Unset):
            after_occurred_at = UNSET
        else:
            after_occurred_at = datetime.datetime.fromisoformat(_after_occurred_at)

        operation_workflow_prerequisite_recovery = cls(
            reason=reason,
            operation=operation,
            milestone=milestone,
            steps=steps,
            after_occurred_at=after_occurred_at,
        )

        return operation_workflow_prerequisite_recovery
