from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_notification_kind import (
    OperationWorkflowReadinessNotificationKind,
    check_operation_workflow_readiness_notification_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_fulfillment_review_escalation import OperationWorkflowFulfillmentReviewEscalation
    from ..models.operation_workflow_fulfillment_review_reminder import OperationWorkflowFulfillmentReviewReminder
    from ..models.operation_workflow_next_action_digest import OperationWorkflowNextActionDigest
    from ..models.operation_workflow_obligation_assessment import OperationWorkflowObligationAssessment
    from ..models.operation_workflow_obligation_event import OperationWorkflowObligationEvent
    from ..models.operation_workflow_obligation_fulfillment_review import OperationWorkflowObligationFulfillmentReview
    from ..models.operation_workflow_obligation_reopening import OperationWorkflowObligationReopening
    from ..models.operation_workflow_playbook_completion_review import OperationWorkflowPlaybookCompletionReview
    from ..models.operation_workflow_playbook_completion_review_event import (
        OperationWorkflowPlaybookCompletionReviewEvent,
    )
    from ..models.operation_workflow_playbook_dependency_recovery import OperationWorkflowPlaybookDependencyRecovery
    from ..models.operation_workflow_playbook_dependency_regression import OperationWorkflowPlaybookDependencyRegression
    from ..models.operation_workflow_playbook_evidence_event import OperationWorkflowPlaybookEvidenceEvent
    from ..models.operation_workflow_playbook_outcome_escalation import OperationWorkflowPlaybookOutcomeEscalation
    from ..models.operation_workflow_playbook_step_assignment import OperationWorkflowPlaybookStepAssignment
    from ..models.operation_workflow_readiness_business_outcome_event import (
        OperationWorkflowReadinessBusinessOutcomeEvent,
    )
    from ..models.operation_workflow_readiness_business_outcome_watch import (
        OperationWorkflowReadinessBusinessOutcomeWatch,
    )
    from ..models.operation_workflow_readiness_change import OperationWorkflowReadinessChange
    from ..models.operation_workflow_readiness_coordination import OperationWorkflowReadinessCoordination
    from ..models.operation_workflow_readiness_due_reminder import OperationWorkflowReadinessDueReminder
    from ..models.operation_workflow_readiness_escalation_response_event import (
        OperationWorkflowReadinessEscalationResponseEvent,
    )
    from ..models.operation_workflow_readiness_impact_event import OperationWorkflowReadinessImpactEvent
    from ..models.operation_workflow_readiness_outcome_escalation import OperationWorkflowReadinessOutcomeEscalation
    from ..models.operation_workflow_readiness_verification_event import OperationWorkflowReadinessVerificationEvent
    from ..models.operation_workflow_readiness_verification_watch import OperationWorkflowReadinessVerificationWatch
    from ..models.operation_workflow_review_response import OperationWorkflowReviewResponse
    from ..models.operation_workflow_review_response_blockage_event import OperationWorkflowReviewResponseBlockageEvent
    from ..models.operation_workflow_review_response_check_in_event import OperationWorkflowReviewResponseCheckInEvent
    from ..models.operation_workflow_review_response_decision_review_event import (
        OperationWorkflowReviewResponseDecisionReviewEvent,
    )
    from ..models.operation_workflow_review_response_handoff_decline_event import (
        OperationWorkflowReviewResponseHandoffDeclineEvent,
    )
    from ..models.operation_workflow_review_response_handoff_reassignment_event import (
        OperationWorkflowReviewResponseHandoffReassignmentEvent,
    )


T = TypeVar("T", bound="OperationWorkflowReadinessNotification")


@_attrs_define
class OperationWorkflowReadinessNotification:
    """Durable advisory readiness notification containing a bounded comparison and reporting Operation identities."""

    id: UUID
    """OperationWorkflowReadinessNotification.id. Stable notification UUID."""
    sequence: int
    """OperationWorkflowReadinessNotification.sequence. Monotonic subscription-local delivery cursor."""
    kind: OperationWorkflowReadinessNotificationKind
    """OperationWorkflowReadinessNotification.kind. Matched subscription notification kind."""
    created_at: datetime.datetime
    """OperationWorkflowReadinessNotification.created_at. Time the observation was converted into a notification."""
    explanation: str
    """OperationWorkflowReadinessNotification.explanation. Human-readable change or unavailable-evaluation
    explanation."""
    fulfillment_review_escalation: OperationWorkflowFulfillmentReviewEscalation | Unset = UNSET
    """Immutable destination delivery receipt for an unresolved review after its actual overdue reminder and
    configured grace period. Reassignment after that reminder cancels escalation for that deadline, including
    reverting an owner change; a new deadline starts a new window. Reaffirmation, reopening, supersession and
    changed policy/deadline suppress stale delivery. Delivery preserves review ownership and original fulfillment
    and does not execute application work."""
    fulfillment_review_reminder: OperationWorkflowFulfillmentReviewReminder | Unset = UNSET
    """Immutable atomic inbox-delivery receipt for one review work-item and deadline. Includes the saved owner,
    reasons, case and evidence context. Identical deadlines do not repeat within the same review cycle; a new
    deadline or review cycle is independently eligible. Delivery does not complete or reopen the original work,
    change its owner, or assert fresh application evidence."""
    fulfillment_review: OperationWorkflowObligationFulfillmentReview | Unset = UNSET
    """Bounded independent observation of the latest fulfilled case/cycle and its original or reaffirmed proof.
    Flags remain latched until explicit reaffirmation or reopening; normal workflow progress is not invalidation.
    Reasons describe retained coverage, not inferred business failure. Identical semantic observations suppress
    owner notices."""
    obligation_reopening: OperationWorkflowObligationReopening | Unset = UNSET
    """Immutable human decision to restore one current-source case/cycle to work, retaining the original
    fulfillment and its application evidence. Owner notices are delivered by the worker, with durable duplicate
    suppression. A subsequent fulfillment requires a report published after this reopening."""
    target_assessment: OperationWorkflowObligationAssessment | Unset = UNSET
    """Coordination assessment for one displayed outstanding source action. Human reviews, ownership acceptance and
    worker checks are distinct from executing application transitions. Free-form response/coordinator plans remain
    fulfillment_unassessed without a declared binding. Deadlines and histories never create dependency guards or
    authorize actions."""
    obligation_receipts: list[OperationWorkflowObligationEvent] | Unset = UNSET
    """Server-written obligation-linked lifecycle receipts saved atomically with this source mutation; bounded
    history is not a complete audit log."""
    obligation_receipts_truncated: bool | Unset = UNSET
    """Older obligation receipts have been pruned from this source record."""
    next_action_digest: OperationWorkflowNextActionDigest | Unset = UNSET
    """One bounded changed-item team digest. Items are advisory snapshots, not application authorization; source
    state may change before reading or publication. Fingerprints exclude polling timestamps and are recorded
    atomically with delivery under the subscription lease. Acknowledgment changes no source obligation."""
    handoff_reassignment: OperationWorkflowReviewResponseHandoffReassignmentEvent | Unset = UNSET
    """Historical handoff notice to the assigned team. Check current ownership before accepting; the original
    review deadline remains unchanged."""
    handoff_decline: OperationWorkflowReviewResponseHandoffDeclineEvent | Unset = UNSET
    """One historical decline notification routed to the previous owner, without automatic reassignment or deadline
    changes. Consult current response ownership before acting."""
    decision_review: OperationWorkflowReviewResponseDecisionReviewEvent | Unset = UNSET
    """One durable revalidation alert per explicit decision, routed to the current response owner without changing
    intent, deadlines or application execution."""
    review_blockage: OperationWorkflowReviewResponseBlockageEvent | Unset = UNSET
    """Sampled blocker changes and positively verified recoveries for a linked response plan. Recovery does not
    resolve the response or establish a business result."""
    review_check_in: OperationWorkflowReviewResponseCheckInEvent | Unset = UNSET
    """OperationWorkflowReviewResponseCheckInEvent. Frozen response plan, linked reviews and routing at one missed
    check-in, separate from business outcome and alert acknowledgment."""
    review_response: OperationWorkflowReviewResponse | Unset = UNSET
    """OperationWorkflowReviewResponse. Human accountability record under the notification coordination revision
    fence, independent of business workflow success."""
    business_impact: OperationWorkflowReadinessImpactEvent | Unset = UNSET
    """OperationWorkflowReadinessImpactEvent. Protected business result monitoring of retained unresolved human
    reviews in a scoped application environment; advisory confidence only."""
    dependency_recovery: OperationWorkflowPlaybookDependencyRecovery | Unset = UNSET
    """OperationWorkflowPlaybookDependencyRecovery. Durable sampled waiting-to-ready observation for previously
    regressed steps, linked to the latest regression notice. Original reviews, queue assignments and clocks remain
    unchanged."""
    dependency_regression: OperationWorkflowPlaybookDependencyRegression | Unset = UNSET
    """OperationWorkflowPlaybookDependencyRegression. Durable sampled dependency regression grouped by affected
    checklist owner. Existing queue assignments, reviews and clocks remain unchanged."""
    playbook_step: OperationWorkflowPlaybookStepAssignment | Unset = UNSET
    """OperationWorkflowPlaybookStepAssignment. Assigned step attention in the existing inbox and follow-up queue.
    Polling preserves manual coordination and bounded ownership history."""
    playbook_review: OperationWorkflowPlaybookCompletionReview | Unset = UNSET
    """OperationWorkflowPlaybookCompletionReview. Bounded retained post-completion monitoring; updates never reopen
    or rewrite the original closure."""
    playbook_review_event: OperationWorkflowPlaybookCompletionReviewEvent | Unset = UNSET
    """OperationWorkflowPlaybookCompletionReviewEvent. Durable result change linked to the original closed
    escalation; conveys no application execution authority."""
    playbook_escalation: OperationWorkflowPlaybookOutcomeEscalation | Unset = UNSET
    """OperationWorkflowPlaybookOutcomeEscalation. Retained assigned deadline follow-up evidence. Polling refreshes
    current observations and the first observed success only; acknowledgment, owner, notes, due times, reminders and
    completion remain human coordination."""
    playbook_evidence: OperationWorkflowPlaybookEvidenceEvent | Unset = UNSET
    """OperationWorkflowPlaybookEvidenceEvent. Opt-in durable checklist evidence monitoring; advisory observations
    preserve human reviews and application business authority."""
    escalation_response: OperationWorkflowReadinessEscalationResponseEvent | Unset = UNSET
    """Single durable acknowledgment-deadline notice for an assigned escalation, with exact scoped source identity.
    Does not automatically schedule a second follow-up or indicate business failure."""
    outcome_escalation: OperationWorkflowReadinessOutcomeEscalation | Unset = UNSET
    """Live evidence for an assigned outcome escalation, separate from immutable deadline delivery evidence and
    human coordination. First observed application success is retained as a flag; it never automatically
    acknowledges or completes work. Monitoring ends with source retention."""
    business_outcome_watch: OperationWorkflowReadinessBusinessOutcomeWatch | Unset = UNSET
    """Retained opt-in watch for one source completion business criterion. Current observations remain advisory;
    deadlines are notified once and unchanged status is suppressed. Source inbox retention and subscription deletion
    end monitoring."""
    business_outcome: OperationWorkflowReadinessBusinessOutcomeEvent | Unset = UNSET
    """Self-contained observed business result or deadline notice for a source completion. Reports never establish
    causation, business failure or execution authority. Source links may expire from inbox retention."""
    verification_watch: OperationWorkflowReadinessVerificationWatch | Unset = UNSET
    """Opt-in watch of captured resolution identities. Verified proofs remain observed for later loss or explicit
    blocker reopening; partial coverage and source inbox retention remain explicit."""
    verification: OperationWorkflowReadinessVerificationEvent | Unset = UNSET
    """Durable in-app notification linked to its original completion watch. Source describes the original
    notification edge; verified does not imply workflow readiness or execution authority."""
    coordination: OperationWorkflowReadinessCoordination | Unset = UNSET
    """OperationWorkflowReadinessCoordination. Shared inbox coordination with optimistic revision checks; omission
    of a follow-up time clears it."""
    change: OperationWorkflowReadinessChange | Unset = UNSET
    """Readiness comparison of the saved edge with fresh retained evidence. Contract and state boundaries are
    explicit."""
    reminder: OperationWorkflowReadinessDueReminder | Unset = UNSET
    """One durable in-app reminder marker for the original notification and follow-up deadline; rescheduling or
    clearing retires it. Does not authorize execution."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        sequence = self.sequence

        kind: str = self.kind

        created_at = self.created_at.isoformat()

        explanation = self.explanation

        fulfillment_review_escalation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.fulfillment_review_escalation, Unset):
            fulfillment_review_escalation = self.fulfillment_review_escalation.to_dict()

        fulfillment_review_reminder: dict[str, Any] | Unset = UNSET
        if not isinstance(self.fulfillment_review_reminder, Unset):
            fulfillment_review_reminder = self.fulfillment_review_reminder.to_dict()

        fulfillment_review: dict[str, Any] | Unset = UNSET
        if not isinstance(self.fulfillment_review, Unset):
            fulfillment_review = self.fulfillment_review.to_dict()

        obligation_reopening: dict[str, Any] | Unset = UNSET
        if not isinstance(self.obligation_reopening, Unset):
            obligation_reopening = self.obligation_reopening.to_dict()

        target_assessment: dict[str, Any] | Unset = UNSET
        if not isinstance(self.target_assessment, Unset):
            target_assessment = self.target_assessment.to_dict()

        obligation_receipts: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.obligation_receipts, Unset):
            obligation_receipts = []
            for obligation_receipts_item_data in self.obligation_receipts:
                obligation_receipts_item = obligation_receipts_item_data.to_dict()
                obligation_receipts.append(obligation_receipts_item)

        obligation_receipts_truncated = self.obligation_receipts_truncated

        next_action_digest: dict[str, Any] | Unset = UNSET
        if not isinstance(self.next_action_digest, Unset):
            next_action_digest = self.next_action_digest.to_dict()

        handoff_reassignment: dict[str, Any] | Unset = UNSET
        if not isinstance(self.handoff_reassignment, Unset):
            handoff_reassignment = self.handoff_reassignment.to_dict()

        handoff_decline: dict[str, Any] | Unset = UNSET
        if not isinstance(self.handoff_decline, Unset):
            handoff_decline = self.handoff_decline.to_dict()

        decision_review: dict[str, Any] | Unset = UNSET
        if not isinstance(self.decision_review, Unset):
            decision_review = self.decision_review.to_dict()

        review_blockage: dict[str, Any] | Unset = UNSET
        if not isinstance(self.review_blockage, Unset):
            review_blockage = self.review_blockage.to_dict()

        review_check_in: dict[str, Any] | Unset = UNSET
        if not isinstance(self.review_check_in, Unset):
            review_check_in = self.review_check_in.to_dict()

        review_response: dict[str, Any] | Unset = UNSET
        if not isinstance(self.review_response, Unset):
            review_response = self.review_response.to_dict()

        business_impact: dict[str, Any] | Unset = UNSET
        if not isinstance(self.business_impact, Unset):
            business_impact = self.business_impact.to_dict()

        dependency_recovery: dict[str, Any] | Unset = UNSET
        if not isinstance(self.dependency_recovery, Unset):
            dependency_recovery = self.dependency_recovery.to_dict()

        dependency_regression: dict[str, Any] | Unset = UNSET
        if not isinstance(self.dependency_regression, Unset):
            dependency_regression = self.dependency_regression.to_dict()

        playbook_step: dict[str, Any] | Unset = UNSET
        if not isinstance(self.playbook_step, Unset):
            playbook_step = self.playbook_step.to_dict()

        playbook_review: dict[str, Any] | Unset = UNSET
        if not isinstance(self.playbook_review, Unset):
            playbook_review = self.playbook_review.to_dict()

        playbook_review_event: dict[str, Any] | Unset = UNSET
        if not isinstance(self.playbook_review_event, Unset):
            playbook_review_event = self.playbook_review_event.to_dict()

        playbook_escalation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.playbook_escalation, Unset):
            playbook_escalation = self.playbook_escalation.to_dict()

        playbook_evidence: dict[str, Any] | Unset = UNSET
        if not isinstance(self.playbook_evidence, Unset):
            playbook_evidence = self.playbook_evidence.to_dict()

        escalation_response: dict[str, Any] | Unset = UNSET
        if not isinstance(self.escalation_response, Unset):
            escalation_response = self.escalation_response.to_dict()

        outcome_escalation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.outcome_escalation, Unset):
            outcome_escalation = self.outcome_escalation.to_dict()

        business_outcome_watch: dict[str, Any] | Unset = UNSET
        if not isinstance(self.business_outcome_watch, Unset):
            business_outcome_watch = self.business_outcome_watch.to_dict()

        business_outcome: dict[str, Any] | Unset = UNSET
        if not isinstance(self.business_outcome, Unset):
            business_outcome = self.business_outcome.to_dict()

        verification_watch: dict[str, Any] | Unset = UNSET
        if not isinstance(self.verification_watch, Unset):
            verification_watch = self.verification_watch.to_dict()

        verification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.verification, Unset):
            verification = self.verification.to_dict()

        coordination: dict[str, Any] | Unset = UNSET
        if not isinstance(self.coordination, Unset):
            coordination = self.coordination.to_dict()

        change: dict[str, Any] | Unset = UNSET
        if not isinstance(self.change, Unset):
            change = self.change.to_dict()

        reminder: dict[str, Any] | Unset = UNSET
        if not isinstance(self.reminder, Unset):
            reminder = self.reminder.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "sequence": sequence,
                "kind": kind,
                "created_at": created_at,
                "explanation": explanation,
            }
        )
        if fulfillment_review_escalation is not UNSET:
            field_dict["fulfillment_review_escalation"] = fulfillment_review_escalation
        if fulfillment_review_reminder is not UNSET:
            field_dict["fulfillment_review_reminder"] = fulfillment_review_reminder
        if fulfillment_review is not UNSET:
            field_dict["fulfillment_review"] = fulfillment_review
        if obligation_reopening is not UNSET:
            field_dict["obligation_reopening"] = obligation_reopening
        if target_assessment is not UNSET:
            field_dict["target_assessment"] = target_assessment
        if obligation_receipts is not UNSET:
            field_dict["obligation_receipts"] = obligation_receipts
        if obligation_receipts_truncated is not UNSET:
            field_dict["obligation_receipts_truncated"] = obligation_receipts_truncated
        if next_action_digest is not UNSET:
            field_dict["next_action_digest"] = next_action_digest
        if handoff_reassignment is not UNSET:
            field_dict["handoff_reassignment"] = handoff_reassignment
        if handoff_decline is not UNSET:
            field_dict["handoff_decline"] = handoff_decline
        if decision_review is not UNSET:
            field_dict["decision_review"] = decision_review
        if review_blockage is not UNSET:
            field_dict["review_blockage"] = review_blockage
        if review_check_in is not UNSET:
            field_dict["review_check_in"] = review_check_in
        if review_response is not UNSET:
            field_dict["review_response"] = review_response
        if business_impact is not UNSET:
            field_dict["business_impact"] = business_impact
        if dependency_recovery is not UNSET:
            field_dict["dependency_recovery"] = dependency_recovery
        if dependency_regression is not UNSET:
            field_dict["dependency_regression"] = dependency_regression
        if playbook_step is not UNSET:
            field_dict["playbook_step"] = playbook_step
        if playbook_review is not UNSET:
            field_dict["playbook_review"] = playbook_review
        if playbook_review_event is not UNSET:
            field_dict["playbook_review_event"] = playbook_review_event
        if playbook_escalation is not UNSET:
            field_dict["playbook_escalation"] = playbook_escalation
        if playbook_evidence is not UNSET:
            field_dict["playbook_evidence"] = playbook_evidence
        if escalation_response is not UNSET:
            field_dict["escalation_response"] = escalation_response
        if outcome_escalation is not UNSET:
            field_dict["outcome_escalation"] = outcome_escalation
        if business_outcome_watch is not UNSET:
            field_dict["business_outcome_watch"] = business_outcome_watch
        if business_outcome is not UNSET:
            field_dict["business_outcome"] = business_outcome
        if verification_watch is not UNSET:
            field_dict["verification_watch"] = verification_watch
        if verification is not UNSET:
            field_dict["verification"] = verification
        if coordination is not UNSET:
            field_dict["coordination"] = coordination
        if change is not UNSET:
            field_dict["change"] = change
        if reminder is not UNSET:
            field_dict["reminder"] = reminder

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_fulfillment_review_escalation import (
            OperationWorkflowFulfillmentReviewEscalation,
        )
        from ..models.operation_workflow_fulfillment_review_reminder import OperationWorkflowFulfillmentReviewReminder
        from ..models.operation_workflow_next_action_digest import OperationWorkflowNextActionDigest
        from ..models.operation_workflow_obligation_assessment import OperationWorkflowObligationAssessment
        from ..models.operation_workflow_obligation_event import OperationWorkflowObligationEvent
        from ..models.operation_workflow_obligation_fulfillment_review import (
            OperationWorkflowObligationFulfillmentReview,
        )
        from ..models.operation_workflow_obligation_reopening import OperationWorkflowObligationReopening
        from ..models.operation_workflow_playbook_completion_review import OperationWorkflowPlaybookCompletionReview
        from ..models.operation_workflow_playbook_completion_review_event import (
            OperationWorkflowPlaybookCompletionReviewEvent,
        )
        from ..models.operation_workflow_playbook_dependency_recovery import OperationWorkflowPlaybookDependencyRecovery
        from ..models.operation_workflow_playbook_dependency_regression import (
            OperationWorkflowPlaybookDependencyRegression,
        )
        from ..models.operation_workflow_playbook_evidence_event import OperationWorkflowPlaybookEvidenceEvent
        from ..models.operation_workflow_playbook_outcome_escalation import OperationWorkflowPlaybookOutcomeEscalation
        from ..models.operation_workflow_playbook_step_assignment import OperationWorkflowPlaybookStepAssignment
        from ..models.operation_workflow_readiness_business_outcome_event import (
            OperationWorkflowReadinessBusinessOutcomeEvent,
        )
        from ..models.operation_workflow_readiness_business_outcome_watch import (
            OperationWorkflowReadinessBusinessOutcomeWatch,
        )
        from ..models.operation_workflow_readiness_change import OperationWorkflowReadinessChange
        from ..models.operation_workflow_readiness_coordination import OperationWorkflowReadinessCoordination
        from ..models.operation_workflow_readiness_due_reminder import OperationWorkflowReadinessDueReminder
        from ..models.operation_workflow_readiness_escalation_response_event import (
            OperationWorkflowReadinessEscalationResponseEvent,
        )
        from ..models.operation_workflow_readiness_impact_event import OperationWorkflowReadinessImpactEvent
        from ..models.operation_workflow_readiness_outcome_escalation import OperationWorkflowReadinessOutcomeEscalation
        from ..models.operation_workflow_readiness_verification_event import OperationWorkflowReadinessVerificationEvent
        from ..models.operation_workflow_readiness_verification_watch import OperationWorkflowReadinessVerificationWatch
        from ..models.operation_workflow_review_response import OperationWorkflowReviewResponse
        from ..models.operation_workflow_review_response_blockage_event import (
            OperationWorkflowReviewResponseBlockageEvent,
        )
        from ..models.operation_workflow_review_response_check_in_event import (
            OperationWorkflowReviewResponseCheckInEvent,
        )
        from ..models.operation_workflow_review_response_decision_review_event import (
            OperationWorkflowReviewResponseDecisionReviewEvent,
        )
        from ..models.operation_workflow_review_response_handoff_decline_event import (
            OperationWorkflowReviewResponseHandoffDeclineEvent,
        )
        from ..models.operation_workflow_review_response_handoff_reassignment_event import (
            OperationWorkflowReviewResponseHandoffReassignmentEvent,
        )

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        sequence = d.pop("sequence")

        kind = check_operation_workflow_readiness_notification_kind(d.pop("kind"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        explanation = d.pop("explanation")

        _fulfillment_review_escalation = d.pop("fulfillment_review_escalation", UNSET)
        fulfillment_review_escalation: OperationWorkflowFulfillmentReviewEscalation | Unset
        if isinstance(_fulfillment_review_escalation, Unset):
            fulfillment_review_escalation = UNSET
        else:
            fulfillment_review_escalation = OperationWorkflowFulfillmentReviewEscalation.from_dict(
                _fulfillment_review_escalation
            )

        _fulfillment_review_reminder = d.pop("fulfillment_review_reminder", UNSET)
        fulfillment_review_reminder: OperationWorkflowFulfillmentReviewReminder | Unset
        if isinstance(_fulfillment_review_reminder, Unset):
            fulfillment_review_reminder = UNSET
        else:
            fulfillment_review_reminder = OperationWorkflowFulfillmentReviewReminder.from_dict(
                _fulfillment_review_reminder
            )

        _fulfillment_review = d.pop("fulfillment_review", UNSET)
        fulfillment_review: OperationWorkflowObligationFulfillmentReview | Unset
        if isinstance(_fulfillment_review, Unset):
            fulfillment_review = UNSET
        else:
            fulfillment_review = OperationWorkflowObligationFulfillmentReview.from_dict(_fulfillment_review)

        _obligation_reopening = d.pop("obligation_reopening", UNSET)
        obligation_reopening: OperationWorkflowObligationReopening | Unset
        if isinstance(_obligation_reopening, Unset):
            obligation_reopening = UNSET
        else:
            obligation_reopening = OperationWorkflowObligationReopening.from_dict(_obligation_reopening)

        _target_assessment = d.pop("target_assessment", UNSET)
        target_assessment: OperationWorkflowObligationAssessment | Unset
        if isinstance(_target_assessment, Unset):
            target_assessment = UNSET
        else:
            target_assessment = OperationWorkflowObligationAssessment.from_dict(_target_assessment)

        _obligation_receipts = d.pop("obligation_receipts", UNSET)
        obligation_receipts: list[OperationWorkflowObligationEvent] | Unset = UNSET
        if _obligation_receipts is not UNSET:
            obligation_receipts = []
            for obligation_receipts_item_data in _obligation_receipts:
                obligation_receipts_item = OperationWorkflowObligationEvent.from_dict(obligation_receipts_item_data)

                obligation_receipts.append(obligation_receipts_item)

        obligation_receipts_truncated = d.pop("obligation_receipts_truncated", UNSET)

        _next_action_digest = d.pop("next_action_digest", UNSET)
        next_action_digest: OperationWorkflowNextActionDigest | Unset
        if isinstance(_next_action_digest, Unset):
            next_action_digest = UNSET
        else:
            next_action_digest = OperationWorkflowNextActionDigest.from_dict(_next_action_digest)

        _handoff_reassignment = d.pop("handoff_reassignment", UNSET)
        handoff_reassignment: OperationWorkflowReviewResponseHandoffReassignmentEvent | Unset
        if isinstance(_handoff_reassignment, Unset):
            handoff_reassignment = UNSET
        else:
            handoff_reassignment = OperationWorkflowReviewResponseHandoffReassignmentEvent.from_dict(
                _handoff_reassignment
            )

        _handoff_decline = d.pop("handoff_decline", UNSET)
        handoff_decline: OperationWorkflowReviewResponseHandoffDeclineEvent | Unset
        if isinstance(_handoff_decline, Unset):
            handoff_decline = UNSET
        else:
            handoff_decline = OperationWorkflowReviewResponseHandoffDeclineEvent.from_dict(_handoff_decline)

        _decision_review = d.pop("decision_review", UNSET)
        decision_review: OperationWorkflowReviewResponseDecisionReviewEvent | Unset
        if isinstance(_decision_review, Unset):
            decision_review = UNSET
        else:
            decision_review = OperationWorkflowReviewResponseDecisionReviewEvent.from_dict(_decision_review)

        _review_blockage = d.pop("review_blockage", UNSET)
        review_blockage: OperationWorkflowReviewResponseBlockageEvent | Unset
        if isinstance(_review_blockage, Unset):
            review_blockage = UNSET
        else:
            review_blockage = OperationWorkflowReviewResponseBlockageEvent.from_dict(_review_blockage)

        _review_check_in = d.pop("review_check_in", UNSET)
        review_check_in: OperationWorkflowReviewResponseCheckInEvent | Unset
        if isinstance(_review_check_in, Unset):
            review_check_in = UNSET
        else:
            review_check_in = OperationWorkflowReviewResponseCheckInEvent.from_dict(_review_check_in)

        _review_response = d.pop("review_response", UNSET)
        review_response: OperationWorkflowReviewResponse | Unset
        if isinstance(_review_response, Unset):
            review_response = UNSET
        else:
            review_response = OperationWorkflowReviewResponse.from_dict(_review_response)

        _business_impact = d.pop("business_impact", UNSET)
        business_impact: OperationWorkflowReadinessImpactEvent | Unset
        if isinstance(_business_impact, Unset):
            business_impact = UNSET
        else:
            business_impact = OperationWorkflowReadinessImpactEvent.from_dict(_business_impact)

        _dependency_recovery = d.pop("dependency_recovery", UNSET)
        dependency_recovery: OperationWorkflowPlaybookDependencyRecovery | Unset
        if isinstance(_dependency_recovery, Unset):
            dependency_recovery = UNSET
        else:
            dependency_recovery = OperationWorkflowPlaybookDependencyRecovery.from_dict(_dependency_recovery)

        _dependency_regression = d.pop("dependency_regression", UNSET)
        dependency_regression: OperationWorkflowPlaybookDependencyRegression | Unset
        if isinstance(_dependency_regression, Unset):
            dependency_regression = UNSET
        else:
            dependency_regression = OperationWorkflowPlaybookDependencyRegression.from_dict(_dependency_regression)

        _playbook_step = d.pop("playbook_step", UNSET)
        playbook_step: OperationWorkflowPlaybookStepAssignment | Unset
        if isinstance(_playbook_step, Unset):
            playbook_step = UNSET
        else:
            playbook_step = OperationWorkflowPlaybookStepAssignment.from_dict(_playbook_step)

        _playbook_review = d.pop("playbook_review", UNSET)
        playbook_review: OperationWorkflowPlaybookCompletionReview | Unset
        if isinstance(_playbook_review, Unset):
            playbook_review = UNSET
        else:
            playbook_review = OperationWorkflowPlaybookCompletionReview.from_dict(_playbook_review)

        _playbook_review_event = d.pop("playbook_review_event", UNSET)
        playbook_review_event: OperationWorkflowPlaybookCompletionReviewEvent | Unset
        if isinstance(_playbook_review_event, Unset):
            playbook_review_event = UNSET
        else:
            playbook_review_event = OperationWorkflowPlaybookCompletionReviewEvent.from_dict(_playbook_review_event)

        _playbook_escalation = d.pop("playbook_escalation", UNSET)
        playbook_escalation: OperationWorkflowPlaybookOutcomeEscalation | Unset
        if isinstance(_playbook_escalation, Unset):
            playbook_escalation = UNSET
        else:
            playbook_escalation = OperationWorkflowPlaybookOutcomeEscalation.from_dict(_playbook_escalation)

        _playbook_evidence = d.pop("playbook_evidence", UNSET)
        playbook_evidence: OperationWorkflowPlaybookEvidenceEvent | Unset
        if isinstance(_playbook_evidence, Unset):
            playbook_evidence = UNSET
        else:
            playbook_evidence = OperationWorkflowPlaybookEvidenceEvent.from_dict(_playbook_evidence)

        _escalation_response = d.pop("escalation_response", UNSET)
        escalation_response: OperationWorkflowReadinessEscalationResponseEvent | Unset
        if isinstance(_escalation_response, Unset):
            escalation_response = UNSET
        else:
            escalation_response = OperationWorkflowReadinessEscalationResponseEvent.from_dict(_escalation_response)

        _outcome_escalation = d.pop("outcome_escalation", UNSET)
        outcome_escalation: OperationWorkflowReadinessOutcomeEscalation | Unset
        if isinstance(_outcome_escalation, Unset):
            outcome_escalation = UNSET
        else:
            outcome_escalation = OperationWorkflowReadinessOutcomeEscalation.from_dict(_outcome_escalation)

        _business_outcome_watch = d.pop("business_outcome_watch", UNSET)
        business_outcome_watch: OperationWorkflowReadinessBusinessOutcomeWatch | Unset
        if isinstance(_business_outcome_watch, Unset):
            business_outcome_watch = UNSET
        else:
            business_outcome_watch = OperationWorkflowReadinessBusinessOutcomeWatch.from_dict(_business_outcome_watch)

        _business_outcome = d.pop("business_outcome", UNSET)
        business_outcome: OperationWorkflowReadinessBusinessOutcomeEvent | Unset
        if isinstance(_business_outcome, Unset):
            business_outcome = UNSET
        else:
            business_outcome = OperationWorkflowReadinessBusinessOutcomeEvent.from_dict(_business_outcome)

        _verification_watch = d.pop("verification_watch", UNSET)
        verification_watch: OperationWorkflowReadinessVerificationWatch | Unset
        if isinstance(_verification_watch, Unset):
            verification_watch = UNSET
        else:
            verification_watch = OperationWorkflowReadinessVerificationWatch.from_dict(_verification_watch)

        _verification = d.pop("verification", UNSET)
        verification: OperationWorkflowReadinessVerificationEvent | Unset
        if isinstance(_verification, Unset):
            verification = UNSET
        else:
            verification = OperationWorkflowReadinessVerificationEvent.from_dict(_verification)

        _coordination = d.pop("coordination", UNSET)
        coordination: OperationWorkflowReadinessCoordination | Unset
        if isinstance(_coordination, Unset):
            coordination = UNSET
        else:
            coordination = OperationWorkflowReadinessCoordination.from_dict(_coordination)

        _change = d.pop("change", UNSET)
        change: OperationWorkflowReadinessChange | Unset
        if isinstance(_change, Unset):
            change = UNSET
        else:
            change = OperationWorkflowReadinessChange.from_dict(_change)

        _reminder = d.pop("reminder", UNSET)
        reminder: OperationWorkflowReadinessDueReminder | Unset
        if isinstance(_reminder, Unset):
            reminder = UNSET
        else:
            reminder = OperationWorkflowReadinessDueReminder.from_dict(_reminder)

        operation_workflow_readiness_notification = cls(
            id=id,
            sequence=sequence,
            kind=kind,
            created_at=created_at,
            explanation=explanation,
            fulfillment_review_escalation=fulfillment_review_escalation,
            fulfillment_review_reminder=fulfillment_review_reminder,
            fulfillment_review=fulfillment_review,
            obligation_reopening=obligation_reopening,
            target_assessment=target_assessment,
            obligation_receipts=obligation_receipts,
            obligation_receipts_truncated=obligation_receipts_truncated,
            next_action_digest=next_action_digest,
            handoff_reassignment=handoff_reassignment,
            handoff_decline=handoff_decline,
            decision_review=decision_review,
            review_blockage=review_blockage,
            review_check_in=review_check_in,
            review_response=review_response,
            business_impact=business_impact,
            dependency_recovery=dependency_recovery,
            dependency_regression=dependency_regression,
            playbook_step=playbook_step,
            playbook_review=playbook_review,
            playbook_review_event=playbook_review_event,
            playbook_escalation=playbook_escalation,
            playbook_evidence=playbook_evidence,
            escalation_response=escalation_response,
            outcome_escalation=outcome_escalation,
            business_outcome_watch=business_outcome_watch,
            business_outcome=business_outcome,
            verification_watch=verification_watch,
            verification=verification,
            coordination=coordination,
            change=change,
            reminder=reminder,
        )

        return operation_workflow_readiness_notification
