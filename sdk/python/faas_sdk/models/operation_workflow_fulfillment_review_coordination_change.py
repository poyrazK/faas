from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_obligation_target_binding import OperationWorkflowObligationTargetBinding


T = TypeVar("T", bound="OperationWorkflowFulfillmentReviewCoordinationChange")


@_attrs_define
class OperationWorkflowFulfillmentReviewCoordinationChange:
    """Immutable explicit ownership/deadline change for one exact pending review cycle, including prior values and
    authenticated actor/reason. Does not modify original work or execute an application transition.

    """

    review_id: str
    binding: OperationWorkflowObligationTargetBinding
    """One obligation-cycle and exact-case target binding, recorded with an authenticated actor and reason.
    Unrelated edits preserve it; a new cycle never inherits it. Current readiness is independently reassessed."""
    fulfilled_at: datetime.datetime
    from_owner: str
    owner: str
    reason: str
    at: datetime.datetime
    actor: str
    from_escalation_owner: str | Unset = UNSET
    previous_escalate_after_seconds: int | Unset = UNSET
    escalation_owner: str | Unset = UNSET
    """New escalation team recorded by this review coordination change; empty or absent means escalation is
    disabled."""
    escalate_after_seconds: int | Unset = UNSET
    """New grace period in seconds recorded by this review coordination change; zero or absent means escalation is
    disabled."""
    previous_due_at: datetime.datetime | Unset = UNSET
    due_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        review_id = self.review_id

        binding = self.binding.to_dict()

        fulfilled_at = self.fulfilled_at.isoformat()

        from_owner = self.from_owner

        owner = self.owner

        reason = self.reason

        at = self.at.isoformat()

        actor = self.actor

        from_escalation_owner = self.from_escalation_owner

        previous_escalate_after_seconds = self.previous_escalate_after_seconds

        escalation_owner = self.escalation_owner

        escalate_after_seconds = self.escalate_after_seconds

        previous_due_at: str | Unset = UNSET
        if not isinstance(self.previous_due_at, Unset):
            previous_due_at = self.previous_due_at.isoformat()

        due_at: str | Unset = UNSET
        if not isinstance(self.due_at, Unset):
            due_at = self.due_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "review_id": review_id,
                "binding": binding,
                "fulfilled_at": fulfilled_at,
                "from_owner": from_owner,
                "owner": owner,
                "reason": reason,
                "at": at,
                "actor": actor,
            }
        )
        if from_escalation_owner is not UNSET:
            field_dict["from_escalation_owner"] = from_escalation_owner
        if previous_escalate_after_seconds is not UNSET:
            field_dict["previous_escalate_after_seconds"] = previous_escalate_after_seconds
        if escalation_owner is not UNSET:
            field_dict["escalation_owner"] = escalation_owner
        if escalate_after_seconds is not UNSET:
            field_dict["escalate_after_seconds"] = escalate_after_seconds
        if previous_due_at is not UNSET:
            field_dict["previous_due_at"] = previous_due_at
        if due_at is not UNSET:
            field_dict["due_at"] = due_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_obligation_target_binding import OperationWorkflowObligationTargetBinding

        d = dict(src_dict)
        review_id = d.pop("review_id")

        binding = OperationWorkflowObligationTargetBinding.from_dict(d.pop("binding"))

        fulfilled_at = datetime.datetime.fromisoformat(d.pop("fulfilled_at"))

        from_owner = d.pop("from_owner")

        owner = d.pop("owner")

        reason = d.pop("reason")

        at = datetime.datetime.fromisoformat(d.pop("at"))

        actor = d.pop("actor")

        from_escalation_owner = d.pop("from_escalation_owner", UNSET)

        previous_escalate_after_seconds = d.pop("previous_escalate_after_seconds", UNSET)

        escalation_owner = d.pop("escalation_owner", UNSET)

        escalate_after_seconds = d.pop("escalate_after_seconds", UNSET)

        _previous_due_at = d.pop("previous_due_at", UNSET)
        previous_due_at: datetime.datetime | Unset
        if isinstance(_previous_due_at, Unset):
            previous_due_at = UNSET
        else:
            previous_due_at = datetime.datetime.fromisoformat(_previous_due_at)

        _due_at = d.pop("due_at", UNSET)
        due_at: datetime.datetime | Unset
        if isinstance(_due_at, Unset):
            due_at = UNSET
        else:
            due_at = datetime.datetime.fromisoformat(_due_at)

        operation_workflow_fulfillment_review_coordination_change = cls(
            review_id=review_id,
            binding=binding,
            fulfilled_at=fulfilled_at,
            from_owner=from_owner,
            owner=owner,
            reason=reason,
            at=at,
            actor=actor,
            from_escalation_owner=from_escalation_owner,
            previous_escalate_after_seconds=previous_escalate_after_seconds,
            escalation_owner=escalation_owner,
            escalate_after_seconds=escalate_after_seconds,
            previous_due_at=previous_due_at,
            due_at=due_at,
        )

        return operation_workflow_fulfillment_review_coordination_change
