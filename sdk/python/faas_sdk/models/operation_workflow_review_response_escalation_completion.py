from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_review_response_escalation_action_review import (
        OperationWorkflowReviewResponseEscalationActionReview,
    )
    from ..models.operation_workflow_review_response_link import OperationWorkflowReviewResponseLink
    from ..models.operation_workflow_review_response_scheduled_review_cancellation import (
        OperationWorkflowReviewResponseScheduledReviewCancellation,
    )
    from ..models.operation_workflow_review_response_scheduled_review_reschedule import (
        OperationWorkflowReviewResponseScheduledReviewReschedule,
    )


T = TypeVar("T", bound="OperationWorkflowReviewResponseEscalationCompletion")


@_attrs_define
class OperationWorkflowReviewResponseEscalationCompletion:
    """Immutable human completion of escalation coordination work, with linked review references and server actor and time.
    Existing overdue markers remain historical.

    """

    acknowledged_at: datetime.datetime
    summary: str
    evidence: list[OperationWorkflowReviewResponseLink]
    at: datetime.datetime
    actor: str
    original_review_due_at: datetime.datetime | Unset = UNSET
    """Original schedule time retained across explicit reschedules, even if older change history is pruned. Legacy
    completions initialize this from their current due time on first reschedule."""
    review_reschedule: OperationWorkflowReviewResponseScheduledReviewReschedule | Unset = UNSET
    """Immutable schedule change audit with previous and replacement due times, completion identity, human reason
    and server actor and time. Retained change history records each reschedule; current completion keeps the latest
    change and original due time."""
    review_cancellation: OperationWorkflowReviewResponseScheduledReviewCancellation | Unset = UNSET
    """Immutable cancellation audit for one pending scheduled evidence check. Retains original completion and due
    timestamps, server actor and time, and human reason. Does not change completion evidence, ownership or review
    obligations."""
    review_due_at: datetime.datetime | Unset = UNSET
    """Current one-shot evidence check time. Only explicit rescheduling changes it; ordinary edits and manual
    evidence reviews do not."""
    scheduled_review_at: datetime.datetime | Unset = UNSET
    """Scheduled evidence check delivery marker committed atomically with its observation and durable inbox
    notification. Existing notification retention bounds apply."""
    review: OperationWorkflowReviewResponseEscalationActionReview | Unset = UNSET
    """Most recent explicit scoped observation of immutable completion evidence. Source indices refer to the
    completion evidence order. A clear observation does not establish causality or business completion; observation
    is not atomic with concurrent application reports. History preserves prior review snapshots within existing
    retention bounds."""

    def to_dict(self) -> dict[str, Any]:
        acknowledged_at = self.acknowledged_at.isoformat()

        summary = self.summary

        evidence = []
        for evidence_item_data in self.evidence:
            evidence_item = evidence_item_data.to_dict()
            evidence.append(evidence_item)

        at = self.at.isoformat()

        actor = self.actor

        original_review_due_at: str | Unset = UNSET
        if not isinstance(self.original_review_due_at, Unset):
            original_review_due_at = self.original_review_due_at.isoformat()

        review_reschedule: dict[str, Any] | Unset = UNSET
        if not isinstance(self.review_reschedule, Unset):
            review_reschedule = self.review_reschedule.to_dict()

        review_cancellation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.review_cancellation, Unset):
            review_cancellation = self.review_cancellation.to_dict()

        review_due_at: str | Unset = UNSET
        if not isinstance(self.review_due_at, Unset):
            review_due_at = self.review_due_at.isoformat()

        scheduled_review_at: str | Unset = UNSET
        if not isinstance(self.scheduled_review_at, Unset):
            scheduled_review_at = self.scheduled_review_at.isoformat()

        review: dict[str, Any] | Unset = UNSET
        if not isinstance(self.review, Unset):
            review = self.review.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "acknowledged_at": acknowledged_at,
                "summary": summary,
                "evidence": evidence,
                "at": at,
                "actor": actor,
            }
        )
        if original_review_due_at is not UNSET:
            field_dict["original_review_due_at"] = original_review_due_at
        if review_reschedule is not UNSET:
            field_dict["review_reschedule"] = review_reschedule
        if review_cancellation is not UNSET:
            field_dict["review_cancellation"] = review_cancellation
        if review_due_at is not UNSET:
            field_dict["review_due_at"] = review_due_at
        if scheduled_review_at is not UNSET:
            field_dict["scheduled_review_at"] = scheduled_review_at
        if review is not UNSET:
            field_dict["review"] = review

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_review_response_escalation_action_review import (
            OperationWorkflowReviewResponseEscalationActionReview,
        )
        from ..models.operation_workflow_review_response_link import OperationWorkflowReviewResponseLink
        from ..models.operation_workflow_review_response_scheduled_review_cancellation import (
            OperationWorkflowReviewResponseScheduledReviewCancellation,
        )
        from ..models.operation_workflow_review_response_scheduled_review_reschedule import (
            OperationWorkflowReviewResponseScheduledReviewReschedule,
        )

        d = dict(src_dict)
        acknowledged_at = datetime.datetime.fromisoformat(d.pop("acknowledged_at"))

        summary = d.pop("summary")

        evidence = []
        _evidence = d.pop("evidence")
        for evidence_item_data in _evidence:
            evidence_item = OperationWorkflowReviewResponseLink.from_dict(evidence_item_data)

            evidence.append(evidence_item)

        at = datetime.datetime.fromisoformat(d.pop("at"))

        actor = d.pop("actor")

        _original_review_due_at = d.pop("original_review_due_at", UNSET)
        original_review_due_at: datetime.datetime | Unset
        if isinstance(_original_review_due_at, Unset):
            original_review_due_at = UNSET
        else:
            original_review_due_at = datetime.datetime.fromisoformat(_original_review_due_at)

        _review_reschedule = d.pop("review_reschedule", UNSET)
        review_reschedule: OperationWorkflowReviewResponseScheduledReviewReschedule | Unset
        if isinstance(_review_reschedule, Unset):
            review_reschedule = UNSET
        else:
            review_reschedule = OperationWorkflowReviewResponseScheduledReviewReschedule.from_dict(_review_reschedule)

        _review_cancellation = d.pop("review_cancellation", UNSET)
        review_cancellation: OperationWorkflowReviewResponseScheduledReviewCancellation | Unset
        if isinstance(_review_cancellation, Unset):
            review_cancellation = UNSET
        else:
            review_cancellation = OperationWorkflowReviewResponseScheduledReviewCancellation.from_dict(
                _review_cancellation
            )

        _review_due_at = d.pop("review_due_at", UNSET)
        review_due_at: datetime.datetime | Unset
        if isinstance(_review_due_at, Unset):
            review_due_at = UNSET
        else:
            review_due_at = datetime.datetime.fromisoformat(_review_due_at)

        _scheduled_review_at = d.pop("scheduled_review_at", UNSET)
        scheduled_review_at: datetime.datetime | Unset
        if isinstance(_scheduled_review_at, Unset):
            scheduled_review_at = UNSET
        else:
            scheduled_review_at = datetime.datetime.fromisoformat(_scheduled_review_at)

        _review = d.pop("review", UNSET)
        review: OperationWorkflowReviewResponseEscalationActionReview | Unset
        if isinstance(_review, Unset):
            review = UNSET
        else:
            review = OperationWorkflowReviewResponseEscalationActionReview.from_dict(_review)

        operation_workflow_review_response_escalation_completion = cls(
            acknowledged_at=acknowledged_at,
            summary=summary,
            evidence=evidence,
            at=at,
            actor=actor,
            original_review_due_at=original_review_due_at,
            review_reschedule=review_reschedule,
            review_cancellation=review_cancellation,
            review_due_at=review_due_at,
            scheduled_review_at=scheduled_review_at,
            review=review,
        )

        return operation_workflow_review_response_escalation_completion
