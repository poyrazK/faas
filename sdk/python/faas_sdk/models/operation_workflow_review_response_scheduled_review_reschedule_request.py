from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowReviewResponseScheduledReviewRescheduleRequest")


@_attrs_define
class OperationWorkflowReviewResponseScheduledReviewRescheduleRequest:
    """Reschedule a pending evidence check using exact current episode, completion and due timestamps. New time must differ
    from current due time and be future within seven days. Preserve parent owner, plan and exact check-in, even if
    overdue; omit parent reviews. Cannot combine with other response actions. Published or canceled checks reject
    rescheduling; no source observation is required.

    """

    decision_at: datetime.datetime
    required_at: datetime.datetime
    handed_off_at: datetime.datetime
    escalated_at: datetime.datetime
    acknowledged_at: datetime.datetime
    completed_at: datetime.datetime
    due_at: datetime.datetime
    new_due_at: datetime.datetime
    reason: str
    """Nonblank UTF-8 explanation for the schedule change, at most 1024 bytes; line breaks and tabs allowed."""

    def to_dict(self) -> dict[str, Any]:
        decision_at = self.decision_at.isoformat()

        required_at = self.required_at.isoformat()

        handed_off_at = self.handed_off_at.isoformat()

        escalated_at = self.escalated_at.isoformat()

        acknowledged_at = self.acknowledged_at.isoformat()

        completed_at = self.completed_at.isoformat()

        due_at = self.due_at.isoformat()

        new_due_at = self.new_due_at.isoformat()

        reason = self.reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "decision_at": decision_at,
                "required_at": required_at,
                "handed_off_at": handed_off_at,
                "escalated_at": escalated_at,
                "acknowledged_at": acknowledged_at,
                "completed_at": completed_at,
                "due_at": due_at,
                "new_due_at": new_due_at,
                "reason": reason,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        decision_at = datetime.datetime.fromisoformat(d.pop("decision_at"))

        required_at = datetime.datetime.fromisoformat(d.pop("required_at"))

        handed_off_at = datetime.datetime.fromisoformat(d.pop("handed_off_at"))

        escalated_at = datetime.datetime.fromisoformat(d.pop("escalated_at"))

        acknowledged_at = datetime.datetime.fromisoformat(d.pop("acknowledged_at"))

        completed_at = datetime.datetime.fromisoformat(d.pop("completed_at"))

        due_at = datetime.datetime.fromisoformat(d.pop("due_at"))

        new_due_at = datetime.datetime.fromisoformat(d.pop("new_due_at"))

        reason = d.pop("reason")

        operation_workflow_review_response_scheduled_review_reschedule_request = cls(
            decision_at=decision_at,
            required_at=required_at,
            handed_off_at=handed_off_at,
            escalated_at=escalated_at,
            acknowledged_at=acknowledged_at,
            completed_at=completed_at,
            due_at=due_at,
            new_due_at=new_due_at,
            reason=reason,
        )

        return operation_workflow_review_response_scheduled_review_reschedule_request
