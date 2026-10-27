from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.workflow_initial_recovery_review_event_status import (
    WorkflowInitialRecoveryReviewEventStatus,
    check_workflow_initial_recovery_review_event_status,
)

T = TypeVar("T", bound="WorkflowInitialRecoveryReviewEvent")


@_attrs_define
class WorkflowInitialRecoveryReviewEvent:
    """Notification carrying an initial recovery review and its captured evidence."""

    status: WorkflowInitialRecoveryReviewEventStatus
    reason: str
    occurred_at: datetime.datetime
    actor_kind: str
    actor_id: str

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        reason = self.reason

        occurred_at = self.occurred_at.isoformat()

        actor_kind = self.actor_kind

        actor_id = self.actor_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "status": status,
                "reason": reason,
                "occurred_at": occurred_at,
                "actor_kind": actor_kind,
                "actor_id": actor_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_workflow_initial_recovery_review_event_status(d.pop("status"))

        reason = d.pop("reason")

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        actor_kind = d.pop("actor_kind")

        actor_id = d.pop("actor_id")

        workflow_initial_recovery_review_event = cls(
            status=status,
            reason=reason,
            occurred_at=occurred_at,
            actor_kind=actor_kind,
            actor_id=actor_id,
        )

        return workflow_initial_recovery_review_event
