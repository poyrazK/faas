from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowReviewResponseEscalationAcknowledgmentRequest")


@_attrs_define
class OperationWorkflowReviewResponseEscalationAcknowledgmentRequest:
    """One immutable coordination commitment for the current escalated, unaccepted handoff. Preserve the parent response
    owner, plan and exact check-in, even if overdue. Cannot combine with a decision, acceptance, decline or
    reassignment. Current handoff and escalation timestamps are required. Due date must be in the future and within
    seven days.

    """

    decision_at: datetime.datetime
    required_at: datetime.datetime
    handed_off_at: datetime.datetime
    escalated_at: datetime.datetime
    coordinator: str
    """Control-free trimmed UTF-8 coordinator or team label, nonblank and limited to 256 bytes. Uses the existing
    authorized operator model; not proof of team membership."""
    plan: str
    """Nonblank UTF-8 next-action plan, limited to 1024 bytes; line breaks and tabs are allowed."""
    due_at: datetime.datetime
    """Future next-action deadline within seven days. This does not extend the handoff acceptance or decision-
    review deadlines."""

    def to_dict(self) -> dict[str, Any]:
        decision_at = self.decision_at.isoformat()

        required_at = self.required_at.isoformat()

        handed_off_at = self.handed_off_at.isoformat()

        escalated_at = self.escalated_at.isoformat()

        coordinator = self.coordinator

        plan = self.plan

        due_at = self.due_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "decision_at": decision_at,
                "required_at": required_at,
                "handed_off_at": handed_off_at,
                "escalated_at": escalated_at,
                "coordinator": coordinator,
                "plan": plan,
                "due_at": due_at,
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

        coordinator = d.pop("coordinator")

        plan = d.pop("plan")

        due_at = datetime.datetime.fromisoformat(d.pop("due_at"))

        operation_workflow_review_response_escalation_acknowledgment_request = cls(
            decision_at=decision_at,
            required_at=required_at,
            handed_off_at=handed_off_at,
            escalated_at=escalated_at,
            coordinator=coordinator,
            plan=plan,
            due_at=due_at,
        )

        return operation_workflow_review_response_escalation_acknowledgment_request
