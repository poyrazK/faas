from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_review_response_handoff_decline import (
        OperationWorkflowReviewResponseHandoffDecline,
    )


T = TypeVar("T", bound="OperationWorkflowReviewResponseHandoffReassignment")


@_attrs_define
class OperationWorkflowReviewResponseHandoffReassignment:
    """Immutable manual routing record for an authorized operator, with preserved decline context and one-time notification
    intent. Ownership assignment is not acceptance.

    """

    decision_at: datetime.datetime
    """OperationWorkflowReviewResponseHandoffReassignment.decision_at. Original reviewed decision timestamp."""
    required_at: datetime.datetime
    """OperationWorkflowReviewResponseHandoffReassignment.required_at. Original review requirement timestamp."""
    at: datetime.datetime
    """OperationWorkflowReviewResponseHandoffReassignment.at. Server handoff time, starting the new acceptance
    clock."""
    owner: str
    """OperationWorkflowReviewResponseHandoffReassignment.owner. New responsible team label; membership is not
    verified."""
    previous_owner: str
    """OperationWorkflowReviewResponseHandoffReassignment.previous_owner. Declining owner before this manual
    transfer."""
    actor: str
    """OperationWorkflowReviewResponseHandoffReassignment.actor. Authenticated operator performing reassignment."""
    reason: str
    """OperationWorkflowReviewResponseHandoffReassignment.reason. Operator reason for choosing the new team."""
    decline: OperationWorkflowReviewResponseHandoffDecline
    """Immutable decline for a responsible team recorded by an authenticated authorized operator, without proof of
    team membership. Retained history carries its one-time notification intent."""
    notified_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponseHandoffReassignment.notified_at. One-time notification marker, bounded by
    retained history and inbox."""

    def to_dict(self) -> dict[str, Any]:
        decision_at = self.decision_at.isoformat()

        required_at = self.required_at.isoformat()

        at = self.at.isoformat()

        owner = self.owner

        previous_owner = self.previous_owner

        actor = self.actor

        reason = self.reason

        decline = self.decline.to_dict()

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
                "decline": decline,
            }
        )
        if notified_at is not UNSET:
            field_dict["notified_at"] = notified_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_review_response_handoff_decline import (
            OperationWorkflowReviewResponseHandoffDecline,
        )

        d = dict(src_dict)
        decision_at = datetime.datetime.fromisoformat(d.pop("decision_at"))

        required_at = datetime.datetime.fromisoformat(d.pop("required_at"))

        at = datetime.datetime.fromisoformat(d.pop("at"))

        owner = d.pop("owner")

        previous_owner = d.pop("previous_owner")

        actor = d.pop("actor")

        reason = d.pop("reason")

        decline = OperationWorkflowReviewResponseHandoffDecline.from_dict(d.pop("decline"))

        _notified_at = d.pop("notified_at", UNSET)
        notified_at: datetime.datetime | Unset
        if isinstance(_notified_at, Unset):
            notified_at = UNSET
        else:
            notified_at = datetime.datetime.fromisoformat(_notified_at)

        operation_workflow_review_response_handoff_reassignment = cls(
            decision_at=decision_at,
            required_at=required_at,
            at=at,
            owner=owner,
            previous_owner=previous_owner,
            actor=actor,
            reason=reason,
            decline=decline,
            notified_at=notified_at,
        )

        return operation_workflow_review_response_handoff_reassignment
