from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_obligation_event_kind import (
    OperationWorkflowObligationEventKind,
    check_operation_workflow_obligation_event_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_fulfillment_review_coordination_change import (
        OperationWorkflowFulfillmentReviewCoordinationChange,
    )
    from ..models.operation_workflow_fulfillment_review_escalation import OperationWorkflowFulfillmentReviewEscalation
    from ..models.operation_workflow_fulfillment_review_reminder import OperationWorkflowFulfillmentReviewReminder
    from ..models.operation_workflow_next_action_case import OperationWorkflowNextActionCase
    from ..models.operation_workflow_obligation_fulfillment import OperationWorkflowObligationFulfillment
    from ..models.operation_workflow_obligation_fulfillment_review import OperationWorkflowObligationFulfillmentReview
    from ..models.operation_workflow_obligation_reaffirmation import OperationWorkflowObligationReaffirmation
    from ..models.operation_workflow_obligation_reopening import OperationWorkflowObligationReopening
    from ..models.operation_workflow_obligation_replacement import OperationWorkflowObligationReplacement
    from ..models.operation_workflow_obligation_target_binding import OperationWorkflowObligationTargetBinding


T = TypeVar("T", bound="OperationWorkflowObligationEvent")


@_attrs_define
class OperationWorkflowObligationEvent:
    """Retained source receipt anchored to the saved obligation; observed_assignment describes the delivered assignment
    snapshot, not a newly audited write. Available event coverage is partial.

    """

    kind: OperationWorkflowObligationEventKind
    at: datetime.datetime
    explanation: str
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
    review_coordination: OperationWorkflowFulfillmentReviewCoordinationChange | Unset = UNSET
    """Immutable explicit ownership/deadline change for one exact pending review cycle, including prior values and
    authenticated actor/reason. Does not modify original work or execute an application transition."""
    fulfillment_review: OperationWorkflowObligationFulfillmentReview | Unset = UNSET
    """Bounded independent observation of the latest fulfilled case/cycle and its original or reaffirmed proof.
    Flags remain latched until explicit reaffirmation or reopening; normal workflow progress is not invalidation.
    Reasons describe retained coverage, not inferred business failure. Identical semantic observations suppress
    owner notices."""
    reaffirmation: OperationWorkflowObligationReaffirmation | Unset = UNSET
    """Immutable human review receipt with a fresh retained application state report. Original fulfillment and
    evidence remain intact; subsequent monitoring uses this proof as its basis. Does not authorize application
    execution or establish current business success. Each retained exact-case lookup scans at most 1000 reports."""
    reopening: OperationWorkflowObligationReopening | Unset = UNSET
    """Immutable human decision to restore one current-source case/cycle to work, retaining the original
    fulfillment and its application evidence. Owner notices are delivered by the worker, with durable duplicate
    suppression. A subsequent fulfillment requires a report published after this reopening."""
    fulfillment: OperationWorkflowObligationFulfillment | Unset = UNSET
    """Immutable human fulfillment receipt for one exact case and obligation cycle, linked to a retained
    application transition report. Does not close a shared source plan or assert business success or execution
    authority. Retained transition lookup scans at most 1000 exact-case reports."""
    target_binding: OperationWorkflowObligationTargetBinding | Unset = UNSET
    """One obligation-cycle and exact-case target binding, recorded with an authenticated actor and reason.
    Unrelated edits preserve it; a new cycle never inherits it. Current readiness is independently reassessed."""
    replacement: OperationWorkflowObligationReplacement | Unset = UNSET
    """Same-source obligation succession recorded atomically with a source change. Links require overlapping saved
    case coverage and removed/new cycles or explicit transition references; a retained terminal same-kind
    predecessor may link a later cycle. Generic new-cycle reasons describe succession rather than business
    causation. Reinstated known identities, unrelated cases and missing history are not linked. These edges do not
    prove fulfillment."""
    cases: list[OperationWorkflowNextActionCase] | Unset = UNSET
    """Affected case identities captured at the source change. Legacy receipts may omit them; case views exclude
    unscoped receipts."""
    action_kind: str | Unset = UNSET
    shared_plan: bool | Unset = UNSET
    obligation_id: str | Unset = UNSET
    """Exact obligation identity for mutation-time receipts; older inferred observations may omit it."""
    actor: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        at = self.at.isoformat()

        explanation = self.explanation

        fulfillment_review_escalation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.fulfillment_review_escalation, Unset):
            fulfillment_review_escalation = self.fulfillment_review_escalation.to_dict()

        fulfillment_review_reminder: dict[str, Any] | Unset = UNSET
        if not isinstance(self.fulfillment_review_reminder, Unset):
            fulfillment_review_reminder = self.fulfillment_review_reminder.to_dict()

        review_coordination: dict[str, Any] | Unset = UNSET
        if not isinstance(self.review_coordination, Unset):
            review_coordination = self.review_coordination.to_dict()

        fulfillment_review: dict[str, Any] | Unset = UNSET
        if not isinstance(self.fulfillment_review, Unset):
            fulfillment_review = self.fulfillment_review.to_dict()

        reaffirmation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.reaffirmation, Unset):
            reaffirmation = self.reaffirmation.to_dict()

        reopening: dict[str, Any] | Unset = UNSET
        if not isinstance(self.reopening, Unset):
            reopening = self.reopening.to_dict()

        fulfillment: dict[str, Any] | Unset = UNSET
        if not isinstance(self.fulfillment, Unset):
            fulfillment = self.fulfillment.to_dict()

        target_binding: dict[str, Any] | Unset = UNSET
        if not isinstance(self.target_binding, Unset):
            target_binding = self.target_binding.to_dict()

        replacement: dict[str, Any] | Unset = UNSET
        if not isinstance(self.replacement, Unset):
            replacement = self.replacement.to_dict()

        cases: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.cases, Unset):
            cases = []
            for cases_item_data in self.cases:
                cases_item = cases_item_data.to_dict()
                cases.append(cases_item)

        action_kind = self.action_kind

        shared_plan = self.shared_plan

        obligation_id = self.obligation_id

        actor = self.actor

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
                "at": at,
                "explanation": explanation,
            }
        )
        if fulfillment_review_escalation is not UNSET:
            field_dict["fulfillment_review_escalation"] = fulfillment_review_escalation
        if fulfillment_review_reminder is not UNSET:
            field_dict["fulfillment_review_reminder"] = fulfillment_review_reminder
        if review_coordination is not UNSET:
            field_dict["review_coordination"] = review_coordination
        if fulfillment_review is not UNSET:
            field_dict["fulfillment_review"] = fulfillment_review
        if reaffirmation is not UNSET:
            field_dict["reaffirmation"] = reaffirmation
        if reopening is not UNSET:
            field_dict["reopening"] = reopening
        if fulfillment is not UNSET:
            field_dict["fulfillment"] = fulfillment
        if target_binding is not UNSET:
            field_dict["target_binding"] = target_binding
        if replacement is not UNSET:
            field_dict["replacement"] = replacement
        if cases is not UNSET:
            field_dict["cases"] = cases
        if action_kind is not UNSET:
            field_dict["action_kind"] = action_kind
        if shared_plan is not UNSET:
            field_dict["shared_plan"] = shared_plan
        if obligation_id is not UNSET:
            field_dict["obligation_id"] = obligation_id
        if actor is not UNSET:
            field_dict["actor"] = actor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_fulfillment_review_coordination_change import (
            OperationWorkflowFulfillmentReviewCoordinationChange,
        )
        from ..models.operation_workflow_fulfillment_review_escalation import (
            OperationWorkflowFulfillmentReviewEscalation,
        )
        from ..models.operation_workflow_fulfillment_review_reminder import OperationWorkflowFulfillmentReviewReminder
        from ..models.operation_workflow_next_action_case import OperationWorkflowNextActionCase
        from ..models.operation_workflow_obligation_fulfillment import OperationWorkflowObligationFulfillment
        from ..models.operation_workflow_obligation_fulfillment_review import (
            OperationWorkflowObligationFulfillmentReview,
        )
        from ..models.operation_workflow_obligation_reaffirmation import OperationWorkflowObligationReaffirmation
        from ..models.operation_workflow_obligation_reopening import OperationWorkflowObligationReopening
        from ..models.operation_workflow_obligation_replacement import OperationWorkflowObligationReplacement
        from ..models.operation_workflow_obligation_target_binding import OperationWorkflowObligationTargetBinding

        d = dict(src_dict)
        kind = check_operation_workflow_obligation_event_kind(d.pop("kind"))

        at = datetime.datetime.fromisoformat(d.pop("at"))

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

        _review_coordination = d.pop("review_coordination", UNSET)
        review_coordination: OperationWorkflowFulfillmentReviewCoordinationChange | Unset
        if isinstance(_review_coordination, Unset):
            review_coordination = UNSET
        else:
            review_coordination = OperationWorkflowFulfillmentReviewCoordinationChange.from_dict(_review_coordination)

        _fulfillment_review = d.pop("fulfillment_review", UNSET)
        fulfillment_review: OperationWorkflowObligationFulfillmentReview | Unset
        if isinstance(_fulfillment_review, Unset):
            fulfillment_review = UNSET
        else:
            fulfillment_review = OperationWorkflowObligationFulfillmentReview.from_dict(_fulfillment_review)

        _reaffirmation = d.pop("reaffirmation", UNSET)
        reaffirmation: OperationWorkflowObligationReaffirmation | Unset
        if isinstance(_reaffirmation, Unset):
            reaffirmation = UNSET
        else:
            reaffirmation = OperationWorkflowObligationReaffirmation.from_dict(_reaffirmation)

        _reopening = d.pop("reopening", UNSET)
        reopening: OperationWorkflowObligationReopening | Unset
        if isinstance(_reopening, Unset):
            reopening = UNSET
        else:
            reopening = OperationWorkflowObligationReopening.from_dict(_reopening)

        _fulfillment = d.pop("fulfillment", UNSET)
        fulfillment: OperationWorkflowObligationFulfillment | Unset
        if isinstance(_fulfillment, Unset):
            fulfillment = UNSET
        else:
            fulfillment = OperationWorkflowObligationFulfillment.from_dict(_fulfillment)

        _target_binding = d.pop("target_binding", UNSET)
        target_binding: OperationWorkflowObligationTargetBinding | Unset
        if isinstance(_target_binding, Unset):
            target_binding = UNSET
        else:
            target_binding = OperationWorkflowObligationTargetBinding.from_dict(_target_binding)

        _replacement = d.pop("replacement", UNSET)
        replacement: OperationWorkflowObligationReplacement | Unset
        if isinstance(_replacement, Unset):
            replacement = UNSET
        else:
            replacement = OperationWorkflowObligationReplacement.from_dict(_replacement)

        _cases = d.pop("cases", UNSET)
        cases: list[OperationWorkflowNextActionCase] | Unset = UNSET
        if _cases is not UNSET:
            cases = []
            for cases_item_data in _cases:
                cases_item = OperationWorkflowNextActionCase.from_dict(cases_item_data)

                cases.append(cases_item)

        action_kind = d.pop("action_kind", UNSET)

        shared_plan = d.pop("shared_plan", UNSET)

        obligation_id = d.pop("obligation_id", UNSET)

        actor = d.pop("actor", UNSET)

        operation_workflow_obligation_event = cls(
            kind=kind,
            at=at,
            explanation=explanation,
            fulfillment_review_escalation=fulfillment_review_escalation,
            fulfillment_review_reminder=fulfillment_review_reminder,
            review_coordination=review_coordination,
            fulfillment_review=fulfillment_review,
            reaffirmation=reaffirmation,
            reopening=reopening,
            fulfillment=fulfillment,
            target_binding=target_binding,
            replacement=replacement,
            cases=cases,
            action_kind=action_kind,
            shared_plan=shared_plan,
            obligation_id=obligation_id,
            actor=actor,
        )

        return operation_workflow_obligation_event
