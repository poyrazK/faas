from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowReviewResponseScheduledReviewCancellation")


@_attrs_define
class OperationWorkflowReviewResponseScheduledReviewCancellation:
    """Immutable cancellation audit for one pending scheduled evidence check. Retains original completion and due
    timestamps, server actor and time, and human reason. Does not change completion evidence, ownership or review
    obligations.

    """

    completed_at: datetime.datetime
    due_at: datetime.datetime
    reason: str
    actor: str
    at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        completed_at = self.completed_at.isoformat()

        due_at = self.due_at.isoformat()

        reason = self.reason

        actor = self.actor

        at = self.at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "completed_at": completed_at,
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

        due_at = datetime.datetime.fromisoformat(d.pop("due_at"))

        reason = d.pop("reason")

        actor = d.pop("actor")

        at = datetime.datetime.fromisoformat(d.pop("at"))

        operation_workflow_review_response_scheduled_review_cancellation = cls(
            completed_at=completed_at,
            due_at=due_at,
            reason=reason,
            actor=actor,
            at=at,
        )

        return operation_workflow_review_response_scheduled_review_cancellation
