from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowFulfillmentReviewCoordinationRequest")


@_attrs_define
class OperationWorkflowFulfillmentReviewCoordinationRequest:
    """Assign and schedule one exact pending fulfillment-review cycle with a reason. Source revision and review fingerprint
    fence stale edits. Preserve source status, owner, note and deadline; do not combine with other actions. Configure an
    optional escalation team/grace period; reassignment after an overdue reminder cancels escalation for that deadline,
    including reverted routing. Removing the deadline disables escalation when policy fields are omitted. Empty owner
    explicitly unassigns; omitted due_at clears the review deadline. Original fulfillment remains intact.

    """

    review_id: str
    review_fingerprint: str
    owner: str
    reason: str
    escalation_owner: str | Unset = UNSET
    """Omission preserves existing policy when the review owner is unchanged and a deadline remains. Empty string
    clears escalation. Nonempty team must differ from the review owner and requires an explicit deadline and
    positive grace period."""
    escalate_after_seconds: int | Unset = UNSET
    """Optional grace period after actual overdue-reminder delivery. Omission preserves the existing value; zero
    clears escalation. Owner reassignment or removing the deadline clears policy when both policy fields are
    omitted."""
    due_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        review_id = self.review_id

        review_fingerprint = self.review_fingerprint

        owner = self.owner

        reason = self.reason

        escalation_owner = self.escalation_owner

        escalate_after_seconds = self.escalate_after_seconds

        due_at: str | Unset = UNSET
        if not isinstance(self.due_at, Unset):
            due_at = self.due_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "review_id": review_id,
                "review_fingerprint": review_fingerprint,
                "owner": owner,
                "reason": reason,
            }
        )
        if escalation_owner is not UNSET:
            field_dict["escalation_owner"] = escalation_owner
        if escalate_after_seconds is not UNSET:
            field_dict["escalate_after_seconds"] = escalate_after_seconds
        if due_at is not UNSET:
            field_dict["due_at"] = due_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        review_id = d.pop("review_id")

        review_fingerprint = d.pop("review_fingerprint")

        owner = d.pop("owner")

        reason = d.pop("reason")

        escalation_owner = d.pop("escalation_owner", UNSET)

        escalate_after_seconds = d.pop("escalate_after_seconds", UNSET)

        _due_at = d.pop("due_at", UNSET)
        due_at: datetime.datetime | Unset
        if isinstance(_due_at, Unset):
            due_at = UNSET
        else:
            due_at = datetime.datetime.fromisoformat(_due_at)

        operation_workflow_fulfillment_review_coordination_request = cls(
            review_id=review_id,
            review_fingerprint=review_fingerprint,
            owner=owner,
            reason=reason,
            escalation_owner=escalation_owner,
            escalate_after_seconds=escalate_after_seconds,
            due_at=due_at,
        )

        return operation_workflow_fulfillment_review_coordination_request
