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
    from ..models.operation_workflow_review_response_check_in_event import OperationWorkflowReviewResponseCheckInEvent


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
        from ..models.operation_workflow_review_response_check_in_event import (
            OperationWorkflowReviewResponseCheckInEvent,
        )

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        sequence = d.pop("sequence")

        kind = check_operation_workflow_readiness_notification_kind(d.pop("kind"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        explanation = d.pop("explanation")

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
