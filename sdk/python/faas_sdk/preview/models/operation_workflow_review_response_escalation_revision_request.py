from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowReviewResponseEscalationRevisionRequest")


@_attrs_define
class OperationWorkflowReviewResponseEscalationRevisionRequest:
    """Explicit scoped review of the current completed coordinator action and creation of a revised commitment only while
    the handoff remains unaccepted and undeclined. Supply current episode timestamps and preserve parent owner, plan and
    exact check-in; omit parent reviews. Cannot combine with other response actions. Evidence unavailable is not clear
    or application success. Requires the recorded non-clear review timestamp and a fresh non-clear recheck; deadline
    must be future and within seven days.

    """

    decision_at: datetime.datetime
    required_at: datetime.datetime
    handed_off_at: datetime.datetime
    escalated_at: datetime.datetime
    acknowledged_at: datetime.datetime
    completed_at: datetime.datetime
    review_at: datetime.datetime
    due_at: datetime.datetime
    coordinator: str
    """Nonblank trimmed control-free UTF-8 coordinator label, at most 256 bytes."""
    plan: str
    """Nonblank revised action plan, at most 1024 UTF-8 bytes; line breaks and tabs allowed."""

    def to_dict(self) -> dict[str, Any]:
        decision_at = self.decision_at.isoformat()

        required_at = self.required_at.isoformat()

        handed_off_at = self.handed_off_at.isoformat()

        escalated_at = self.escalated_at.isoformat()

        acknowledged_at = self.acknowledged_at.isoformat()

        completed_at = self.completed_at.isoformat()

        review_at = self.review_at.isoformat()

        due_at = self.due_at.isoformat()

        coordinator = self.coordinator

        plan = self.plan

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "decision_at": decision_at,
                "required_at": required_at,
                "handed_off_at": handed_off_at,
                "escalated_at": escalated_at,
                "acknowledged_at": acknowledged_at,
                "completed_at": completed_at,
                "review_at": review_at,
                "due_at": due_at,
                "coordinator": coordinator,
                "plan": plan,
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

        review_at = datetime.datetime.fromisoformat(d.pop("review_at"))

        due_at = datetime.datetime.fromisoformat(d.pop("due_at"))

        coordinator = d.pop("coordinator")

        plan = d.pop("plan")

        operation_workflow_review_response_escalation_revision_request = cls(
            decision_at=decision_at,
            required_at=required_at,
            handed_off_at=handed_off_at,
            escalated_at=escalated_at,
            acknowledged_at=acknowledged_at,
            completed_at=completed_at,
            review_at=review_at,
            due_at=due_at,
            coordinator=coordinator,
            plan=plan,
        )

        return operation_workflow_review_response_escalation_revision_request
