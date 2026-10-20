from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowReviewResponseHandoffDecline")


@_attrs_define
class OperationWorkflowReviewResponseHandoffDecline:
    """Immutable decline for a responsible team recorded by an authenticated authorized operator, without proof of team
    membership. Retained history carries its one-time notification intent.

    """

    decision_at: datetime.datetime
    """OperationWorkflowReviewResponseHandoffDecline.decision_at. Reviewed decision timestamp."""
    required_at: datetime.datetime
    """OperationWorkflowReviewResponseHandoffDecline.required_at. Original review requirement timestamp."""
    at: datetime.datetime
    """OperationWorkflowReviewResponseHandoffDecline.at. Server decline time."""
    owner: str
    """OperationWorkflowReviewResponseHandoffDecline.owner. Declining responsible team."""
    previous_owner: str
    """OperationWorkflowReviewResponseHandoffDecline.previous_owner. Previous owner to notify for manual routing."""
    actor: str
    """OperationWorkflowReviewResponseHandoffDecline.actor. Authenticated operator recording decline."""
    reason: str
    """OperationWorkflowReviewResponseHandoffDecline.reason. Operator reason; immutable after recording."""
    notified_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponseHandoffDecline.notified_at. One-time notification publication marker; bounded
    history or inbox retention can limit delivery."""

    def to_dict(self) -> dict[str, Any]:
        decision_at = self.decision_at.isoformat()

        required_at = self.required_at.isoformat()

        at = self.at.isoformat()

        owner = self.owner

        previous_owner = self.previous_owner

        actor = self.actor

        reason = self.reason

        notified_at: str | Unset = UNSET
        if not isinstance(self.notified_at, Unset):
            notified_at = self.notified_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "decision_at": decision_at,
                "required_at": required_at,
                "at": at,
                "owner": owner,
                "previous_owner": previous_owner,
                "actor": actor,
                "reason": reason,
            }
        )
        if notified_at is not UNSET:
            field_dict["notified_at"] = notified_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        decision_at = datetime.datetime.fromisoformat(d.pop("decision_at"))

        required_at = datetime.datetime.fromisoformat(d.pop("required_at"))

        at = datetime.datetime.fromisoformat(d.pop("at"))

        owner = d.pop("owner")

        previous_owner = d.pop("previous_owner")

        actor = d.pop("actor")

        reason = d.pop("reason")

        _notified_at = d.pop("notified_at", UNSET)
        notified_at: datetime.datetime | Unset
        if isinstance(_notified_at, Unset):
            notified_at = UNSET
        else:
            notified_at = datetime.datetime.fromisoformat(_notified_at)

        operation_workflow_review_response_handoff_decline = cls(
            decision_at=decision_at,
            required_at=required_at,
            at=at,
            owner=owner,
            previous_owner=previous_owner,
            actor=actor,
            reason=reason,
            notified_at=notified_at,
        )

        return operation_workflow_review_response_handoff_decline
