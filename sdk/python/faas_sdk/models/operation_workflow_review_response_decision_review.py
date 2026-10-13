from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_review_response_escalation_acknowledgment import (
        OperationWorkflowReviewResponseEscalationAcknowledgment,
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
    from ..models.operation_workflow_review_response_health import OperationWorkflowReviewResponseHealth


T = TypeVar("T", bound="OperationWorkflowReviewResponseDecisionReview")


@_attrs_define
class OperationWorkflowReviewResponseDecisionReview:
    """First changed observation for one explicit response decision. Remains latched until a new explicit decision;
    ordinary edits, acknowledgment and subsequent recovery do not clear it.

    """

    decision_at: datetime.datetime
    """OperationWorkflowReviewResponseDecisionReview.decision_at. Time of the decision that needs revalidation."""
    required_at: datetime.datetime
    """OperationWorkflowReviewResponseDecisionReview.required_at. First sampled condition change, committed with
    its one-time inbox alert."""
    changes: list[str]
    """OperationWorkflowReviewResponseDecisionReview.changes. Differences from the immutable reviewed snapshot,
    including unavailable sources without claiming business failure."""
    health: OperationWorkflowReviewResponseHealth
    """Sampled advisory response-plan condition. Clear means no observed blockers or review obligations, not
    business completion. Sampling stops on response resolution."""
    escalation_acknowledgment: OperationWorkflowReviewResponseEscalationAcknowledgment | Unset = UNSET
    """Immutable human escalation acknowledgment with a coordinator commitment, recorded actor and time.
    Notification acknowledgment alone does not create this record. The system overdue marker and explicit human
    completion may be appended without changing commitment fields."""
    acceptance_escalation_owner: str | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.acceptance_escalation_owner. Escalation destination pinned at
    the actual handoff. Missing legacy destinations are not retroactively assigned; a destination equal to current
    ownership receives only the ordinary overdue notice."""
    acceptance_escalated_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.acceptance_escalated_at. One-time escalation delivery marker
    committed atomically with the overdue marker and both inbox notices. Reset only by a new handoff."""
    handed_off_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.handed_off_at. Current handoff timestamp; required by
    acceptance and decline after manual reassignment. Legacy automatic handoffs can omit it."""
    reassignment: OperationWorkflowReviewResponseHandoffReassignment | Unset = UNSET
    """Immutable manual routing record for an authorized operator, with preserved decline context and one-time
    notification intent. Ownership assignment is not acceptance."""
    reassignment_required: bool | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.reassignment_required. Declining team still owns the
    unresolved review; manual owner reassignment clears this flag without erasing decline history."""
    decline: OperationWorkflowReviewResponseHandoffDecline | Unset = UNSET
    """Immutable decline for a responsible team recorded by an authenticated authorized operator, without proof of
    team membership. Retained history carries its one-time notification intent."""
    acceptance_due_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.acceptance_due_at. Acceptance deadline pinned from the actual
    handoff time and effective window, separate from the unchanged decision-review deadline."""
    acceptance_deadline_notified_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.acceptance_deadline_notified_at. One-time unaccepted handoff
    alert marker, independent of notification acknowledgment."""
    acceptance: OperationWorkflowReviewResponseHandoffAcceptance | Unset = UNSET
    """Immutable acceptance by an authenticated authorized operator for the responsible team, without verifying
    team membership or satisfying the original review deadline."""
    initial_owner: str | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.initial_owner. Team responsible when this review episode
    began; unknown legacy ownership prevents automatic handoff."""
    owner_changed: bool | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.owner_changed. Ownership changed during this episode,
    including a move away and back; prevents automatic handoff."""
    fallback_owner: str | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.fallback_owner. Distinct fallback team pinned when this
    episode began; future policy edits do not rewrite it."""
    previous_owner: str | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.previous_owner. Responsible team immediately before the
    deadline evaluation."""
    owner: str | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.owner. Responsible team after the deadline evaluation, whether
    handoff occurred or was protected."""
    reassigned: bool | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.reassigned. Whether the overdue evaluation transferred both
    response and inbox ownership."""
    due_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.due_at. Immutable deadline pinned from required_at and the
    response review window; legacy flags are backfilled."""
    deadline_notified_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.deadline_notified_at. One-time overdue alert marker;
    acknowledgment and ordinary edits do not fulfill revalidation."""

    def to_dict(self) -> dict[str, Any]:
        decision_at = self.decision_at.isoformat()

        required_at = self.required_at.isoformat()

        changes = self.changes

        health = self.health.to_dict()

        escalation_acknowledgment: dict[str, Any] | Unset = UNSET
        if not isinstance(self.escalation_acknowledgment, Unset):
            escalation_acknowledgment = self.escalation_acknowledgment.to_dict()

        acceptance_escalation_owner = self.acceptance_escalation_owner

        acceptance_escalated_at: str | Unset = UNSET
        if not isinstance(self.acceptance_escalated_at, Unset):
            acceptance_escalated_at = self.acceptance_escalated_at.isoformat()

        handed_off_at: str | Unset = UNSET
        if not isinstance(self.handed_off_at, Unset):
            handed_off_at = self.handed_off_at.isoformat()

        reassignment: dict[str, Any] | Unset = UNSET
        if not isinstance(self.reassignment, Unset):
            reassignment = self.reassignment.to_dict()

        reassignment_required = self.reassignment_required

        decline: dict[str, Any] | Unset = UNSET
        if not isinstance(self.decline, Unset):
            decline = self.decline.to_dict()

        acceptance_due_at: str | Unset = UNSET
        if not isinstance(self.acceptance_due_at, Unset):
            acceptance_due_at = self.acceptance_due_at.isoformat()

        acceptance_deadline_notified_at: str | Unset = UNSET
        if not isinstance(self.acceptance_deadline_notified_at, Unset):
            acceptance_deadline_notified_at = self.acceptance_deadline_notified_at.isoformat()

        acceptance: dict[str, Any] | Unset = UNSET
        if not isinstance(self.acceptance, Unset):
            acceptance = self.acceptance.to_dict()

        initial_owner = self.initial_owner

        owner_changed = self.owner_changed

        fallback_owner = self.fallback_owner

        previous_owner = self.previous_owner

        owner = self.owner

        reassigned = self.reassigned

        due_at: str | Unset = UNSET
        if not isinstance(self.due_at, Unset):
            due_at = self.due_at.isoformat()

        deadline_notified_at: str | Unset = UNSET
        if not isinstance(self.deadline_notified_at, Unset):
            deadline_notified_at = self.deadline_notified_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "decision_at": decision_at,
                "required_at": required_at,
                "changes": changes,
                "health": health,
            }
        )
        if escalation_acknowledgment is not UNSET:
            field_dict["escalation_acknowledgment"] = escalation_acknowledgment
        if acceptance_escalation_owner is not UNSET:
            field_dict["acceptance_escalation_owner"] = acceptance_escalation_owner
        if acceptance_escalated_at is not UNSET:
            field_dict["acceptance_escalated_at"] = acceptance_escalated_at
        if handed_off_at is not UNSET:
            field_dict["handed_off_at"] = handed_off_at
        if reassignment is not UNSET:
            field_dict["reassignment"] = reassignment
        if reassignment_required is not UNSET:
            field_dict["reassignment_required"] = reassignment_required
        if decline is not UNSET:
            field_dict["decline"] = decline
        if acceptance_due_at is not UNSET:
            field_dict["acceptance_due_at"] = acceptance_due_at
        if acceptance_deadline_notified_at is not UNSET:
            field_dict["acceptance_deadline_notified_at"] = acceptance_deadline_notified_at
        if acceptance is not UNSET:
            field_dict["acceptance"] = acceptance
        if initial_owner is not UNSET:
            field_dict["initial_owner"] = initial_owner
        if owner_changed is not UNSET:
            field_dict["owner_changed"] = owner_changed
        if fallback_owner is not UNSET:
            field_dict["fallback_owner"] = fallback_owner
        if previous_owner is not UNSET:
            field_dict["previous_owner"] = previous_owner
        if owner is not UNSET:
            field_dict["owner"] = owner
        if reassigned is not UNSET:
            field_dict["reassigned"] = reassigned
        if due_at is not UNSET:
            field_dict["due_at"] = due_at
        if deadline_notified_at is not UNSET:
            field_dict["deadline_notified_at"] = deadline_notified_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_review_response_escalation_acknowledgment import (
            OperationWorkflowReviewResponseEscalationAcknowledgment,
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
        from ..models.operation_workflow_review_response_health import OperationWorkflowReviewResponseHealth

        d = dict(src_dict)
        decision_at = datetime.datetime.fromisoformat(d.pop("decision_at"))

        required_at = datetime.datetime.fromisoformat(d.pop("required_at"))

        changes = cast(list[str], d.pop("changes"))

        health = OperationWorkflowReviewResponseHealth.from_dict(d.pop("health"))

        _escalation_acknowledgment = d.pop("escalation_acknowledgment", UNSET)
        escalation_acknowledgment: OperationWorkflowReviewResponseEscalationAcknowledgment | Unset
        if isinstance(_escalation_acknowledgment, Unset):
            escalation_acknowledgment = UNSET
        else:
            escalation_acknowledgment = OperationWorkflowReviewResponseEscalationAcknowledgment.from_dict(
                _escalation_acknowledgment
            )

        acceptance_escalation_owner = d.pop("acceptance_escalation_owner", UNSET)

        _acceptance_escalated_at = d.pop("acceptance_escalated_at", UNSET)
        acceptance_escalated_at: datetime.datetime | Unset
        if isinstance(_acceptance_escalated_at, Unset):
            acceptance_escalated_at = UNSET
        else:
            acceptance_escalated_at = datetime.datetime.fromisoformat(_acceptance_escalated_at)

        _handed_off_at = d.pop("handed_off_at", UNSET)
        handed_off_at: datetime.datetime | Unset
        if isinstance(_handed_off_at, Unset):
            handed_off_at = UNSET
        else:
            handed_off_at = datetime.datetime.fromisoformat(_handed_off_at)

        _reassignment = d.pop("reassignment", UNSET)
        reassignment: OperationWorkflowReviewResponseHandoffReassignment | Unset
        if isinstance(_reassignment, Unset):
            reassignment = UNSET
        else:
            reassignment = OperationWorkflowReviewResponseHandoffReassignment.from_dict(_reassignment)

        reassignment_required = d.pop("reassignment_required", UNSET)

        _decline = d.pop("decline", UNSET)
        decline: OperationWorkflowReviewResponseHandoffDecline | Unset
        if isinstance(_decline, Unset):
            decline = UNSET
        else:
            decline = OperationWorkflowReviewResponseHandoffDecline.from_dict(_decline)

        _acceptance_due_at = d.pop("acceptance_due_at", UNSET)
        acceptance_due_at: datetime.datetime | Unset
        if isinstance(_acceptance_due_at, Unset):
            acceptance_due_at = UNSET
        else:
            acceptance_due_at = datetime.datetime.fromisoformat(_acceptance_due_at)

        _acceptance_deadline_notified_at = d.pop("acceptance_deadline_notified_at", UNSET)
        acceptance_deadline_notified_at: datetime.datetime | Unset
        if isinstance(_acceptance_deadline_notified_at, Unset):
            acceptance_deadline_notified_at = UNSET
        else:
            acceptance_deadline_notified_at = datetime.datetime.fromisoformat(_acceptance_deadline_notified_at)

        _acceptance = d.pop("acceptance", UNSET)
        acceptance: OperationWorkflowReviewResponseHandoffAcceptance | Unset
        if isinstance(_acceptance, Unset):
            acceptance = UNSET
        else:
            acceptance = OperationWorkflowReviewResponseHandoffAcceptance.from_dict(_acceptance)

        initial_owner = d.pop("initial_owner", UNSET)

        owner_changed = d.pop("owner_changed", UNSET)

        fallback_owner = d.pop("fallback_owner", UNSET)

        previous_owner = d.pop("previous_owner", UNSET)

        owner = d.pop("owner", UNSET)

        reassigned = d.pop("reassigned", UNSET)

        _due_at = d.pop("due_at", UNSET)
        due_at: datetime.datetime | Unset
        if isinstance(_due_at, Unset):
            due_at = UNSET
        else:
            due_at = datetime.datetime.fromisoformat(_due_at)

        _deadline_notified_at = d.pop("deadline_notified_at", UNSET)
        deadline_notified_at: datetime.datetime | Unset
        if isinstance(_deadline_notified_at, Unset):
            deadline_notified_at = UNSET
        else:
            deadline_notified_at = datetime.datetime.fromisoformat(_deadline_notified_at)

        operation_workflow_review_response_decision_review = cls(
            decision_at=decision_at,
            required_at=required_at,
            changes=changes,
            health=health,
            escalation_acknowledgment=escalation_acknowledgment,
            acceptance_escalation_owner=acceptance_escalation_owner,
            acceptance_escalated_at=acceptance_escalated_at,
            handed_off_at=handed_off_at,
            reassignment=reassignment,
            reassignment_required=reassignment_required,
            decline=decline,
            acceptance_due_at=acceptance_due_at,
            acceptance_deadline_notified_at=acceptance_deadline_notified_at,
            acceptance=acceptance,
            initial_owner=initial_owner,
            owner_changed=owner_changed,
            fallback_owner=fallback_owner,
            previous_owner=previous_owner,
            owner=owner,
            reassigned=reassigned,
            due_at=due_at,
            deadline_notified_at=deadline_notified_at,
        )

        return operation_workflow_review_response_decision_review
