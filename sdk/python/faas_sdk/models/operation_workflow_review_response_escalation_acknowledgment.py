from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowReviewResponseEscalationAcknowledgment")


@_attrs_define
class OperationWorkflowReviewResponseEscalationAcknowledgment:
    """Immutable human escalation acknowledgment with a coordinator commitment, recorded actor and time. Notification
    acknowledgment alone does not create this record. Only the system overdue delivery marker may change.

    """

    coordinator: str
    plan: str
    due_at: datetime.datetime
    at: datetime.datetime
    actor: str
    overdue_notified_at: datetime.datetime | Unset = UNSET
    """One-time follow-up publication marker. Stored atomically with its inbox notice and mirrored in retained
    history; human update timestamps remain unchanged."""

    def to_dict(self) -> dict[str, Any]:
        coordinator = self.coordinator

        plan = self.plan

        due_at = self.due_at.isoformat()

        at = self.at.isoformat()

        actor = self.actor

        overdue_notified_at: str | Unset = UNSET
        if not isinstance(self.overdue_notified_at, Unset):
            overdue_notified_at = self.overdue_notified_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "coordinator": coordinator,
                "plan": plan,
                "due_at": due_at,
                "at": at,
                "actor": actor,
            }
        )
        if overdue_notified_at is not UNSET:
            field_dict["overdue_notified_at"] = overdue_notified_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        coordinator = d.pop("coordinator")

        plan = d.pop("plan")

        due_at = datetime.datetime.fromisoformat(d.pop("due_at"))

        at = datetime.datetime.fromisoformat(d.pop("at"))

        actor = d.pop("actor")

        _overdue_notified_at = d.pop("overdue_notified_at", UNSET)
        overdue_notified_at: datetime.datetime | Unset
        if isinstance(_overdue_notified_at, Unset):
            overdue_notified_at = UNSET
        else:
            overdue_notified_at = datetime.datetime.fromisoformat(_overdue_notified_at)

        operation_workflow_review_response_escalation_acknowledgment = cls(
            coordinator=coordinator,
            plan=plan,
            due_at=due_at,
            at=at,
            actor=actor,
            overdue_notified_at=overdue_notified_at,
        )

        return operation_workflow_review_response_escalation_acknowledgment
