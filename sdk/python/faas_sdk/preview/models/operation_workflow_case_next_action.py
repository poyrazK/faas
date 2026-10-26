from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_case_next_action_kind import (
    OperationWorkflowCaseNextActionKind,
    check_operation_workflow_case_next_action_kind,
)
from ..models.operation_workflow_case_next_action_trigger import (
    OperationWorkflowCaseNextActionTrigger,
    check_operation_workflow_case_next_action_trigger,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_action_obligation import OperationWorkflowActionObligation
    from ..models.operation_workflow_obligation_fulfillment_review import OperationWorkflowObligationFulfillmentReview
    from ..models.operation_workflow_review_response_link import OperationWorkflowReviewResponseLink


T = TypeVar("T", bound="OperationWorkflowCaseNextAction")


@_attrs_define
class OperationWorkflowCaseNextAction:
    """One current coordination obligation or suggestion for an exact workflow case, including the responsible label, its
    own deadline and a source inbox link. Shared plans may span cases; evidence includes only references matching this
    case. This does not authorize application execution or infer business completion.

    """

    kind: OperationWorkflowCaseNextActionKind
    summary: str
    trigger: OperationWorkflowCaseNextActionTrigger
    """Human obligation, automatic scheduled worker check or optional operator suggestion. Opening controls does
    not perform the action."""
    overdue: bool
    """This action's own due time is at or before coordination_checked_at. Undated suggestions are never overdue."""
    subscription_id: UUID
    sequence: int
    fulfillment_review: OperationWorkflowObligationFulfillmentReview | Unset = UNSET
    """Bounded independent observation of the latest fulfilled case/cycle and its original or reaffirmed proof.
    Flags remain latched until explicit reaffirmation or reopening; normal workflow progress is not invalidation.
    Reasons describe retained coverage, not inferred business failure. Identical semantic observations suppress
    owner notices."""
    obligation: OperationWorkflowActionObligation | Unset = UNSET
    """Stable source-and-kind identity derived from the persisted obligation epoch, including dependency-review
    revision where available. Ownership changes and deadline edits preserve the identity except a newly scheduled
    response check-in, which defines a new check-in obligation. Older saved actions may omit identity."""
    owner: str | Unset = UNSET
    due_at: datetime.datetime | Unset = UNSET
    evidence: list[OperationWorkflowReviewResponseLink] | Unset = UNSET
    shared_plan: bool | Unset = UNSET
    """The originating plan or completion has references to other workflow cases. Case evidence is filtered; the
    source controls act on the shared record."""

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        summary = self.summary

        trigger: str = self.trigger

        overdue = self.overdue

        subscription_id = str(self.subscription_id)

        sequence = self.sequence

        fulfillment_review: dict[str, Any] | Unset = UNSET
        if not isinstance(self.fulfillment_review, Unset):
            fulfillment_review = self.fulfillment_review.to_dict()

        obligation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.obligation, Unset):
            obligation = self.obligation.to_dict()

        owner = self.owner

        due_at: str | Unset = UNSET
        if not isinstance(self.due_at, Unset):
            due_at = self.due_at.isoformat()

        evidence: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.evidence, Unset):
            evidence = []
            for evidence_item_data in self.evidence:
                evidence_item = evidence_item_data.to_dict()
                evidence.append(evidence_item)

        shared_plan = self.shared_plan

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
                "summary": summary,
                "trigger": trigger,
                "overdue": overdue,
                "subscription_id": subscription_id,
                "sequence": sequence,
            }
        )
        if fulfillment_review is not UNSET:
            field_dict["fulfillment_review"] = fulfillment_review
        if obligation is not UNSET:
            field_dict["obligation"] = obligation
        if owner is not UNSET:
            field_dict["owner"] = owner
        if due_at is not UNSET:
            field_dict["due_at"] = due_at
        if evidence is not UNSET:
            field_dict["evidence"] = evidence
        if shared_plan is not UNSET:
            field_dict["shared_plan"] = shared_plan

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_action_obligation import OperationWorkflowActionObligation
        from ..models.operation_workflow_obligation_fulfillment_review import (
            OperationWorkflowObligationFulfillmentReview,
        )
        from ..models.operation_workflow_review_response_link import OperationWorkflowReviewResponseLink

        d = dict(src_dict)
        kind = check_operation_workflow_case_next_action_kind(d.pop("kind"))

        summary = d.pop("summary")

        trigger = check_operation_workflow_case_next_action_trigger(d.pop("trigger"))

        overdue = d.pop("overdue")

        subscription_id = UUID(d.pop("subscription_id"))

        sequence = d.pop("sequence")

        _fulfillment_review = d.pop("fulfillment_review", UNSET)
        fulfillment_review: OperationWorkflowObligationFulfillmentReview | Unset
        if isinstance(_fulfillment_review, Unset):
            fulfillment_review = UNSET
        else:
            fulfillment_review = OperationWorkflowObligationFulfillmentReview.from_dict(_fulfillment_review)

        _obligation = d.pop("obligation", UNSET)
        obligation: OperationWorkflowActionObligation | Unset
        if isinstance(_obligation, Unset):
            obligation = UNSET
        else:
            obligation = OperationWorkflowActionObligation.from_dict(_obligation)

        owner = d.pop("owner", UNSET)

        _due_at = d.pop("due_at", UNSET)
        due_at: datetime.datetime | Unset
        if isinstance(_due_at, Unset):
            due_at = UNSET
        else:
            due_at = datetime.datetime.fromisoformat(_due_at)

        _evidence = d.pop("evidence", UNSET)
        evidence: list[OperationWorkflowReviewResponseLink] | Unset = UNSET
        if _evidence is not UNSET:
            evidence = []
            for evidence_item_data in _evidence:
                evidence_item = OperationWorkflowReviewResponseLink.from_dict(evidence_item_data)

                evidence.append(evidence_item)

        shared_plan = d.pop("shared_plan", UNSET)

        operation_workflow_case_next_action = cls(
            kind=kind,
            summary=summary,
            trigger=trigger,
            overdue=overdue,
            subscription_id=subscription_id,
            sequence=sequence,
            fulfillment_review=fulfillment_review,
            obligation=obligation,
            owner=owner,
            due_at=due_at,
            evidence=evidence,
            shared_plan=shared_plan,
        )

        return operation_workflow_case_next_action
