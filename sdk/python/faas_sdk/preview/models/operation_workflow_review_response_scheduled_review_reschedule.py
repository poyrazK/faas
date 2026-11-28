from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowReviewResponseScheduledReviewReschedule")


@_attrs_define
class OperationWorkflowReviewResponseScheduledReviewReschedule:
    """Immutable schedule change audit with previous and replacement due times, completion identity, human reason and
    server actor and time. Retained change history records each reschedule; current completion keeps the latest change
    and original due time.

    """

    completed_at: datetime.datetime
    previous_due_at: datetime.datetime
    due_at: datetime.datetime
    reason: str
    actor: str
    at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        completed_at = self.completed_at.isoformat()

        previous_due_at = self.previous_due_at.isoformat()

        due_at = self.due_at.isoformat()

        reason = self.reason

        actor = self.actor

        at = self.at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "completed_at": completed_at,
                "previous_due_at": previous_due_at,
                "due_at": due_at,
                "reason": reason,
                "actor": actor,
                "at": at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        completed_at = datetime.datetime.fromisoformat(d.pop("completed_at"))

        previous_due_at = datetime.datetime.fromisoformat(d.pop("previous_due_at"))

        due_at = datetime.datetime.fromisoformat(d.pop("due_at"))

        reason = d.pop("reason")

        actor = d.pop("actor")

        at = datetime.datetime.fromisoformat(d.pop("at"))

        operation_workflow_review_response_scheduled_review_reschedule = cls(
            completed_at=completed_at,
            previous_due_at=previous_due_at,
            due_at=due_at,
            reason=reason,
            actor=actor,
            at=at,
        )

        return operation_workflow_review_response_scheduled_review_reschedule
