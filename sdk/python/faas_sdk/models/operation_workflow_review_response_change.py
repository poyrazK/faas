from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_review_response_change_reason import (
    OperationWorkflowReviewResponseChangeReason,
    check_operation_workflow_review_response_change_reason,
)
from ..models.operation_workflow_review_response_change_status import (
    OperationWorkflowReviewResponseChangeStatus,
    check_operation_workflow_review_response_change_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_review_response_decision import OperationWorkflowReviewResponseDecision
    from ..models.operation_workflow_review_response_escalation_acknowledgment import (
        OperationWorkflowReviewResponseEscalationAcknowledgment,
    )
    from ..models.operation_workflow_review_response_escalation_action_review import (
        OperationWorkflowReviewResponseEscalationActionReview,
    )
    from ..models.operation_workflow_review_response_escalation_completion import (
        OperationWorkflowReviewResponseEscalationCompletion,
    )
    from ..models.operation_workflow_review_response_handoff_acceptance import (
        OperationWorkflowReviewResponseHandoffAcceptance,
    )
    from ..models.operation_workflow_review_response_handoff_decline import (
        OperationWorkflowReviewResponseHandoffDecline,
    )
    from ..models.operation_workflow_review_response_handoff_reassignment import (
        OperationWorkflowReviewResponseHandoffReassignment,
    )
    from ..models.operation_workflow_review_response_scheduled_review_cancellation import (
        OperationWorkflowReviewResponseScheduledReviewCancellation,
    )
    from ..models.operation_workflow_review_response_scheduled_review_reschedule import (
        OperationWorkflowReviewResponseScheduledReviewReschedule,
    )


T = TypeVar("T", bound="OperationWorkflowReviewResponseChange")


@_attrs_define
class OperationWorkflowReviewResponseChange:
    """OperationWorkflowReviewResponseChange. Authenticated response update snapshot."""

    owner: str
    """OperationWorkflowReviewResponseChange.owner. Responsible team label; trimmed control-free UTF-8 limited to
    256 bytes."""
    plan: str
    """OperationWorkflowReviewResponseChange.plan. Human response plan; nonblank UTF-8 limited to 1024 bytes. Line
    breaks and tabs are allowed."""
    status: OperationWorkflowReviewResponseChangeStatus
    """OperationWorkflowReviewResponseChange.status. Active ownership or human resolution after a fresh scoped
    check of linked source reviews."""
    at: datetime.datetime
    """OperationWorkflowReviewResponseChange.at. Server change time."""
    actor: str
    """OperationWorkflowReviewResponseChange.actor. Authenticated response editor."""
    scheduled_review_reschedule: OperationWorkflowReviewResponseScheduledReviewReschedule | Unset = UNSET
    """Immutable schedule change audit with previous and replacement due times, completion identity, human reason
    and server actor and time. Retained change history records each reschedule; current completion keeps the latest
    change and original due time."""
    scheduled_review_cancellation: OperationWorkflowReviewResponseScheduledReviewCancellation | Unset = UNSET
    """Immutable cancellation audit for one pending scheduled evidence check. Retains original completion and due
    timestamps, server actor and time, and human reason. Does not change completion evidence, ownership or review
    obligations."""
    escalation_action_review: OperationWorkflowReviewResponseEscalationActionReview | Unset = UNSET
    """Most recent explicit scoped observation of immutable completion evidence. Source indices refer to the
    completion evidence order. A clear observation does not establish causality or business completion; observation
    is not atomic with concurrent application reports. History preserves prior review snapshots within existing
    retention bounds."""
    escalation_completion: OperationWorkflowReviewResponseEscalationCompletion | Unset = UNSET
    """Immutable human completion of escalation coordination work, with linked review references and server actor
    and time. Existing overdue markers remain historical."""
    escalation_acknowledgment: OperationWorkflowReviewResponseEscalationAcknowledgment | Unset = UNSET
    """Immutable human escalation acknowledgment with a coordinator commitment, recorded actor and time.
    Notification acknowledgment alone does not create this record. The system overdue marker and explicit human
    completion may be appended without changing commitment fields."""
    handoff_acceptance_escalation_owner: str | Unset = UNSET
    """OperationWorkflowReviewResponseChange.handoff_acceptance_escalation_owner. Optional control-free UTF-8 team
    label for future missed acceptance deadlines. Omission preserves policy, empty string clears it; the destination
    is pinned at each actual handoff. Must differ from the parent request owner when explicitly configured."""
    handoff_reassignment: OperationWorkflowReviewResponseHandoffReassignment | Unset = UNSET
    """Immutable manual routing record for an authorized operator, with preserved decline context and one-time
    notification intent. Ownership assignment is not acceptance."""
    handoff_decline: OperationWorkflowReviewResponseHandoffDecline | Unset = UNSET
    """Immutable decline for a responsible team recorded by an authenticated authorized operator, without proof of
    team membership. Retained history carries its one-time notification intent."""
    handoff_accept_after_seconds: int | Unset = UNSET
    """OperationWorkflowReviewResponseChange.handoff_accept_after_seconds. Future handoff acceptance window,
    defaulting to 3600 seconds. Request omission preserves current policy; active acceptance deadlines stay pinned.
   """
    handoff_acceptance: OperationWorkflowReviewResponseHandoffAcceptance | Unset = UNSET
    """Immutable acceptance by an authenticated authorized operator for the responsible team, without verifying
    team membership or satisfying the original review deadline."""
    decision_review_fallback_owner: str | Unset = UNSET
    """OperationWorkflowReviewResponseChange.decision_review_fallback_owner. Optional distinct fallback team for
    future overdue decision reviews. Request omission preserves policy and empty string clears it; active episodes
    retain their pinned fallback."""
    decision_review_after_seconds: int | Unset = UNSET
    """OperationWorkflowReviewResponseChange.decision_review_after_seconds. Future decision revalidation window;
    defaults to 86400 seconds. Existing deadlines remain pinned; request omission preserves current policy."""
    decision: OperationWorkflowReviewResponseDecision | Unset = UNSET
    """Immutable human decision with accountable actor and the reviewed health snapshot. Application execution
    remains application-owned."""
    fallback_owner: str | Unset = UNSET
    """OperationWorkflowReviewResponseChange.fallback_owner. Fallback policy captured for this response change."""
    reason: OperationWorkflowReviewResponseChangeReason | Unset = UNSET
    """OperationWorkflowReviewResponseChange.reason. Explicit human edit or periodic missed-check-in event; absent
    in older history."""
    check_in_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponseChange.check_in_at. Required future check-in for active responses; absent
    when resolved. Replaces inbox attention time without changing the original alert response deadline."""

    def to_dict(self) -> dict[str, Any]:
        owner = self.owner

        plan = self.plan

        status: str = self.status

        at = self.at.isoformat()

        actor = self.actor

        scheduled_review_reschedule: dict[str, Any] | Unset = UNSET
        if not isinstance(self.scheduled_review_reschedule, Unset):
            scheduled_review_reschedule = self.scheduled_review_reschedule.to_dict()

        scheduled_review_cancellation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.scheduled_review_cancellation, Unset):
            scheduled_review_cancellation = self.scheduled_review_cancellation.to_dict()

        escalation_action_review: dict[str, Any] | Unset = UNSET
        if not isinstance(self.escalation_action_review, Unset):
            escalation_action_review = self.escalation_action_review.to_dict()

        escalation_completion: dict[str, Any] | Unset = UNSET
        if not isinstance(self.escalation_completion, Unset):
            escalation_completion = self.escalation_completion.to_dict()

        escalation_acknowledgment: dict[str, Any] | Unset = UNSET
        if not isinstance(self.escalation_acknowledgment, Unset):
            escalation_acknowledgment = self.escalation_acknowledgment.to_dict()

        handoff_acceptance_escalation_owner = self.handoff_acceptance_escalation_owner

        handoff_reassignment: dict[str, Any] | Unset = UNSET
        if not isinstance(self.handoff_reassignment, Unset):
            handoff_reassignment = self.handoff_reassignment.to_dict()

        handoff_decline: dict[str, Any] | Unset = UNSET
        if not isinstance(self.handoff_decline, Unset):
            handoff_decline = self.handoff_decline.to_dict()

        handoff_accept_after_seconds = self.handoff_accept_after_seconds

        handoff_acceptance: dict[str, Any] | Unset = UNSET
        if not isinstance(self.handoff_acceptance, Unset):
            handoff_acceptance = self.handoff_acceptance.to_dict()

        decision_review_fallback_owner = self.decision_review_fallback_owner

        decision_review_after_seconds = self.decision_review_after_seconds

        decision: dict[str, Any] | Unset = UNSET
        if not isinstance(self.decision, Unset):
            decision = self.decision.to_dict()

        fallback_owner = self.fallback_owner

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        check_in_at: str | Unset = UNSET
        if not isinstance(self.check_in_at, Unset):
            check_in_at = self.check_in_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "owner": owner,
                "plan": plan,
                "status": status,
                "at": at,
                "actor": actor,
            }
        )
        if scheduled_review_reschedule is not UNSET:
            field_dict["scheduled_review_reschedule"] = scheduled_review_reschedule
        if scheduled_review_cancellation is not UNSET:
            field_dict["scheduled_review_cancellation"] = scheduled_review_cancellation
        if escalation_action_review is not UNSET:
            field_dict["escalation_action_review"] = escalation_action_review
        if escalation_completion is not UNSET:
            field_dict["escalation_completion"] = escalation_completion
        if escalation_acknowledgment is not UNSET:
            field_dict["escalation_acknowledgment"] = escalation_acknowledgment
        if handoff_acceptance_escalation_owner is not UNSET:
            field_dict["handoff_acceptance_escalation_owner"] = handoff_acceptance_escalation_owner
        if handoff_reassignment is not UNSET:
            field_dict["handoff_reassignment"] = handoff_reassignment
        if handoff_decline is not UNSET:
            field_dict["handoff_decline"] = handoff_decline
        if handoff_accept_after_seconds is not UNSET:
            field_dict["handoff_accept_after_seconds"] = handoff_accept_after_seconds
        if handoff_acceptance is not UNSET:
            field_dict["handoff_acceptance"] = handoff_acceptance
        if decision_review_fallback_owner is not UNSET:
            field_dict["decision_review_fallback_owner"] = decision_review_fallback_owner
        if decision_review_after_seconds is not UNSET:
            field_dict["decision_review_after_seconds"] = decision_review_after_seconds
        if decision is not UNSET:
            field_dict["decision"] = decision
        if fallback_owner is not UNSET:
            field_dict["fallback_owner"] = fallback_owner
        if reason is not UNSET:
            field_dict["reason"] = reason
        if check_in_at is not UNSET:
            field_dict["check_in_at"] = check_in_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_review_response_decision import OperationWorkflowReviewResponseDecision
        from ..models.operation_workflow_review_response_escalation_acknowledgment import (
            OperationWorkflowReviewResponseEscalationAcknowledgment,
        )
        from ..models.operation_workflow_review_response_escalation_action_review import (
            OperationWorkflowReviewResponseEscalationActionReview,
        )
        from ..models.operation_workflow_review_response_escalation_completion import (
            OperationWorkflowReviewResponseEscalationCompletion,
        )
        from ..models.operation_workflow_review_response_handoff_acceptance import (
            OperationWorkflowReviewResponseHandoffAcceptance,
        )
        from ..models.operation_workflow_review_response_handoff_decline import (
            OperationWorkflowReviewResponseHandoffDecline,
        )
        from ..models.operation_workflow_review_response_handoff_reassignment import (
            OperationWorkflowReviewResponseHandoffReassignment,
        )
        from ..models.operation_workflow_review_response_scheduled_review_cancellation import (
            OperationWorkflowReviewResponseScheduledReviewCancellation,
        )
        from ..models.operation_workflow_review_response_scheduled_review_reschedule import (
            OperationWorkflowReviewResponseScheduledReviewReschedule,
        )

        d = dict(src_dict)
        owner = d.pop("owner")

        plan = d.pop("plan")

        status = check_operation_workflow_review_response_change_status(d.pop("status"))

        at = datetime.datetime.fromisoformat(d.pop("at"))

        actor = d.pop("actor")

        _scheduled_review_reschedule = d.pop("scheduled_review_reschedule", UNSET)
        scheduled_review_reschedule: OperationWorkflowReviewResponseScheduledReviewReschedule | Unset
        if isinstance(_scheduled_review_reschedule, Unset):
            scheduled_review_reschedule = UNSET
        else:
            scheduled_review_reschedule = OperationWorkflowReviewResponseScheduledReviewReschedule.from_dict(
                _scheduled_review_reschedule
            )

        _scheduled_review_cancellation = d.pop("scheduled_review_cancellation", UNSET)
        scheduled_review_cancellation: OperationWorkflowReviewResponseScheduledReviewCancellation | Unset
        if isinstance(_scheduled_review_cancellation, Unset):
            scheduled_review_cancellation = UNSET
        else:
            scheduled_review_cancellation = OperationWorkflowReviewResponseScheduledReviewCancellation.from_dict(
                _scheduled_review_cancellation
            )

        _escalation_action_review = d.pop("escalation_action_review", UNSET)
        escalation_action_review: OperationWorkflowReviewResponseEscalationActionReview | Unset
        if isinstance(_escalation_action_review, Unset):
            escalation_action_review = UNSET
        else:
            escalation_action_review = OperationWorkflowReviewResponseEscalationActionReview.from_dict(
                _escalation_action_review
            )

        _escalation_completion = d.pop("escalation_completion", UNSET)
        escalation_completion: OperationWorkflowReviewResponseEscalationCompletion | Unset
        if isinstance(_escalation_completion, Unset):
            escalation_completion = UNSET
        else:
            escalation_completion = OperationWorkflowReviewResponseEscalationCompletion.from_dict(
                _escalation_completion
            )

        _escalation_acknowledgment = d.pop("escalation_acknowledgment", UNSET)
        escalation_acknowledgment: OperationWorkflowReviewResponseEscalationAcknowledgment | Unset
        if isinstance(_escalation_acknowledgment, Unset):
            escalation_acknowledgment = UNSET
        else:
            escalation_acknowledgment = OperationWorkflowReviewResponseEscalationAcknowledgment.from_dict(
                _escalation_acknowledgment
            )

        handoff_acceptance_escalation_owner = d.pop("handoff_acceptance_escalation_owner", UNSET)

        _handoff_reassignment = d.pop("handoff_reassignment", UNSET)
        handoff_reassignment: OperationWorkflowReviewResponseHandoffReassignment | Unset
        if isinstance(_handoff_reassignment, Unset):
            handoff_reassignment = UNSET
        else:
            handoff_reassignment = OperationWorkflowReviewResponseHandoffReassignment.from_dict(_handoff_reassignment)

        _handoff_decline = d.pop("handoff_decline", UNSET)
        handoff_decline: OperationWorkflowReviewResponseHandoffDecline | Unset
        if isinstance(_handoff_decline, Unset):
            handoff_decline = UNSET
        else:
            handoff_decline = OperationWorkflowReviewResponseHandoffDecline.from_dict(_handoff_decline)

        handoff_accept_after_seconds = d.pop("handoff_accept_after_seconds", UNSET)

        _handoff_acceptance = d.pop("handoff_acceptance", UNSET)
        handoff_acceptance: OperationWorkflowReviewResponseHandoffAcceptance | Unset
        if isinstance(_handoff_acceptance, Unset):
            handoff_acceptance = UNSET
        else:
            handoff_acceptance = OperationWorkflowReviewResponseHandoffAcceptance.from_dict(_handoff_acceptance)

        decision_review_fallback_owner = d.pop("decision_review_fallback_owner", UNSET)

        decision_review_after_seconds = d.pop("decision_review_after_seconds", UNSET)

        _decision = d.pop("decision", UNSET)
        decision: OperationWorkflowReviewResponseDecision | Unset
        if isinstance(_decision, Unset):
            decision = UNSET
        else:
            decision = OperationWorkflowReviewResponseDecision.from_dict(_decision)

        fallback_owner = d.pop("fallback_owner", UNSET)

        _reason = d.pop("reason", UNSET)
        reason: OperationWorkflowReviewResponseChangeReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_operation_workflow_review_response_change_reason(_reason)

        _check_in_at = d.pop("check_in_at", UNSET)
        check_in_at: datetime.datetime | Unset
        if isinstance(_check_in_at, Unset):
            check_in_at = UNSET
        else:
            check_in_at = datetime.datetime.fromisoformat(_check_in_at)

        operation_workflow_review_response_change = cls(
            owner=owner,
            plan=plan,
            status=status,
            at=at,
            actor=actor,
            scheduled_review_reschedule=scheduled_review_reschedule,
            scheduled_review_cancellation=scheduled_review_cancellation,
            escalation_action_review=escalation_action_review,
            escalation_completion=escalation_completion,
            escalation_acknowledgment=escalation_acknowledgment,
            handoff_acceptance_escalation_owner=handoff_acceptance_escalation_owner,
            handoff_reassignment=handoff_reassignment,
            handoff_decline=handoff_decline,
            handoff_accept_after_seconds=handoff_accept_after_seconds,
            handoff_acceptance=handoff_acceptance,
            decision_review_fallback_owner=decision_review_fallback_owner,
            decision_review_after_seconds=decision_review_after_seconds,
            decision=decision,
            fallback_owner=fallback_owner,
            reason=reason,
            check_in_at=check_in_at,
        )

        return operation_workflow_review_response_change
