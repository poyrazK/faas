from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_coordination_status import (
    OperationWorkflowReadinessCoordinationStatus,
    check_operation_workflow_readiness_coordination_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_fulfillment_review_coordination_change import (
        OperationWorkflowFulfillmentReviewCoordinationChange,
    )
    from ..models.operation_workflow_fulfillment_review_escalation import OperationWorkflowFulfillmentReviewEscalation
    from ..models.operation_workflow_fulfillment_review_reminder import OperationWorkflowFulfillmentReviewReminder
    from ..models.operation_workflow_obligation_fulfillment import OperationWorkflowObligationFulfillment
    from ..models.operation_workflow_obligation_fulfillment_review import OperationWorkflowObligationFulfillmentReview
    from ..models.operation_workflow_obligation_reaffirmation import OperationWorkflowObligationReaffirmation
    from ..models.operation_workflow_obligation_reopening import OperationWorkflowObligationReopening
    from ..models.operation_workflow_obligation_target_binding import OperationWorkflowObligationTargetBinding
    from ..models.operation_workflow_readiness_completion import OperationWorkflowReadinessCompletion


T = TypeVar("T", bound="OperationWorkflowReadinessCoordination")


@_attrs_define
class OperationWorkflowReadinessCoordination:
    """OperationWorkflowReadinessCoordination. Shared inbox coordination with optimistic revision checks; omission of a
    follow-up time clears it.

    """

    revision: int
    """OperationWorkflowReadinessCoordination.revision. Shared coordination value; does not alter application
    evidence."""
    status: OperationWorkflowReadinessCoordinationStatus
    """OperationWorkflowReadinessCoordination.status. Shared coordination value; does not alter application
    evidence."""
    owner: str
    """OperationWorkflowReadinessCoordination.owner. Shared coordination value; does not alter application
    evidence."""
    note: str
    """OperationWorkflowReadinessCoordination.note. Shared coordination value; does not alter application evidence."""
    updated_at: datetime.datetime
    """OperationWorkflowReadinessCoordination.updated_at. Shared coordination value; does not alter application
    evidence."""
    updated_by: str
    """OperationWorkflowReadinessCoordination.updated_by. Shared coordination value; does not alter application
    evidence."""
    fulfillment_review_escalations: list[OperationWorkflowFulfillmentReviewEscalation] | Unset = UNSET
    """Immutable escalation delivery receipts. One delivery per review ID/deadline across destination/policy edits
    and restoring deadlines; capacity exhaustion stops further escalation delivery for this retained source."""
    fulfillment_review_reminders: list[OperationWorkflowFulfillmentReviewReminder] | Unset = UNSET
    """Immutable delivery receipts; each review deadline is notified once, even if restored later. At capacity no
    further reminders are published for this retained source."""
    review_coordination_changes: list[OperationWorkflowFulfillmentReviewCoordinationChange] | Unset = UNSET
    """Immutable review-cycle ownership/deadline changes. Capacity exhaustion rejects further writes without
    pruning decisions."""
    fulfillment_reviews: list[OperationWorkflowObligationFulfillmentReview] | Unset = UNSET
    reaffirmations: list[OperationWorkflowObligationReaffirmation] | Unset = UNSET
    """Immutable human reaffirmations with fresh application evidence; capacity exhaustion rejects further writes."""
    reopenings: list[OperationWorkflowObligationReopening] | Unset = UNSET
    """Retained immutable human reopening receipts. Capacity exhaustion rejects further reopening without pruning
    prior decisions."""
    fulfillments: list[OperationWorkflowObligationFulfillment] | Unset = UNSET
    """Immutable exact-case cycle fulfillment receipts. Capacity exhaustion rejects further fulfillment; receipts
    are never pruned into reopened work."""
    target_bindings: list[OperationWorkflowObligationTargetBinding] | Unset = UNSET
    completion: OperationWorkflowReadinessCompletion | Unset = UNSET
    """Immutable recorded follow-up completion with server-derived actor, write time and advisory pre-write
    evidence check. Retained with the original notification."""
    follow_up_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReadinessCoordination.follow_up_at. Shared coordination value; does not alter application
    evidence."""
    acknowledged_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReadinessCoordination.acknowledged_at. Shared coordination value; does not alter
    application evidence."""
    acknowledged_by: str | Unset = UNSET
    """OperationWorkflowReadinessCoordination.acknowledged_by. Shared coordination value; does not alter
    application evidence."""

    def to_dict(self) -> dict[str, Any]:
        revision = self.revision

        status: str = self.status

        owner = self.owner

        note = self.note

        updated_at = self.updated_at.isoformat()

        updated_by = self.updated_by

        fulfillment_review_escalations: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.fulfillment_review_escalations, Unset):
            fulfillment_review_escalations = []
            for fulfillment_review_escalations_item_data in self.fulfillment_review_escalations:
                fulfillment_review_escalations_item = fulfillment_review_escalations_item_data.to_dict()
                fulfillment_review_escalations.append(fulfillment_review_escalations_item)

        fulfillment_review_reminders: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.fulfillment_review_reminders, Unset):
            fulfillment_review_reminders = []
            for fulfillment_review_reminders_item_data in self.fulfillment_review_reminders:
                fulfillment_review_reminders_item = fulfillment_review_reminders_item_data.to_dict()
                fulfillment_review_reminders.append(fulfillment_review_reminders_item)

        review_coordination_changes: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.review_coordination_changes, Unset):
            review_coordination_changes = []
            for review_coordination_changes_item_data in self.review_coordination_changes:
                review_coordination_changes_item = review_coordination_changes_item_data.to_dict()
                review_coordination_changes.append(review_coordination_changes_item)

        fulfillment_reviews: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.fulfillment_reviews, Unset):
            fulfillment_reviews = []
            for fulfillment_reviews_item_data in self.fulfillment_reviews:
                fulfillment_reviews_item = fulfillment_reviews_item_data.to_dict()
                fulfillment_reviews.append(fulfillment_reviews_item)

        reaffirmations: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.reaffirmations, Unset):
            reaffirmations = []
            for reaffirmations_item_data in self.reaffirmations:
                reaffirmations_item = reaffirmations_item_data.to_dict()
                reaffirmations.append(reaffirmations_item)

        reopenings: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.reopenings, Unset):
            reopenings = []
            for reopenings_item_data in self.reopenings:
                reopenings_item = reopenings_item_data.to_dict()
                reopenings.append(reopenings_item)

        fulfillments: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.fulfillments, Unset):
            fulfillments = []
            for fulfillments_item_data in self.fulfillments:
                fulfillments_item = fulfillments_item_data.to_dict()
                fulfillments.append(fulfillments_item)

        target_bindings: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.target_bindings, Unset):
            target_bindings = []
            for target_bindings_item_data in self.target_bindings:
                target_bindings_item = target_bindings_item_data.to_dict()
                target_bindings.append(target_bindings_item)

        completion: dict[str, Any] | Unset = UNSET
        if not isinstance(self.completion, Unset):
            completion = self.completion.to_dict()

        follow_up_at: str | Unset = UNSET
        if not isinstance(self.follow_up_at, Unset):
            follow_up_at = self.follow_up_at.isoformat()

        acknowledged_at: str | Unset = UNSET
        if not isinstance(self.acknowledged_at, Unset):
            acknowledged_at = self.acknowledged_at.isoformat()

        acknowledged_by = self.acknowledged_by

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "revision": revision,
                "status": status,
                "owner": owner,
                "note": note,
                "updated_at": updated_at,
                "updated_by": updated_by,
            }
        )
        if fulfillment_review_escalations is not UNSET:
            field_dict["fulfillment_review_escalations"] = fulfillment_review_escalations
        if fulfillment_review_reminders is not UNSET:
            field_dict["fulfillment_review_reminders"] = fulfillment_review_reminders
        if review_coordination_changes is not UNSET:
            field_dict["review_coordination_changes"] = review_coordination_changes
        if fulfillment_reviews is not UNSET:
            field_dict["fulfillment_reviews"] = fulfillment_reviews
        if reaffirmations is not UNSET:
            field_dict["reaffirmations"] = reaffirmations
        if reopenings is not UNSET:
            field_dict["reopenings"] = reopenings
        if fulfillments is not UNSET:
            field_dict["fulfillments"] = fulfillments
        if target_bindings is not UNSET:
            field_dict["target_bindings"] = target_bindings
        if completion is not UNSET:
            field_dict["completion"] = completion
        if follow_up_at is not UNSET:
            field_dict["follow_up_at"] = follow_up_at
        if acknowledged_at is not UNSET:
            field_dict["acknowledged_at"] = acknowledged_at
        if acknowledged_by is not UNSET:
            field_dict["acknowledged_by"] = acknowledged_by

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
        from ..models.operation_workflow_obligation_fulfillment import OperationWorkflowObligationFulfillment
        from ..models.operation_workflow_obligation_fulfillment_review import (
            OperationWorkflowObligationFulfillmentReview,
        )
        from ..models.operation_workflow_obligation_reaffirmation import OperationWorkflowObligationReaffirmation
        from ..models.operation_workflow_obligation_reopening import OperationWorkflowObligationReopening
        from ..models.operation_workflow_obligation_target_binding import OperationWorkflowObligationTargetBinding
        from ..models.operation_workflow_readiness_completion import OperationWorkflowReadinessCompletion

        d = dict(src_dict)
        revision = d.pop("revision")

        status = check_operation_workflow_readiness_coordination_status(d.pop("status"))

        owner = d.pop("owner")

        note = d.pop("note")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        updated_by = d.pop("updated_by")

        _fulfillment_review_escalations = d.pop("fulfillment_review_escalations", UNSET)
        fulfillment_review_escalations: list[OperationWorkflowFulfillmentReviewEscalation] | Unset = UNSET
        if _fulfillment_review_escalations is not UNSET:
            fulfillment_review_escalations = []
            for fulfillment_review_escalations_item_data in _fulfillment_review_escalations:
                fulfillment_review_escalations_item = OperationWorkflowFulfillmentReviewEscalation.from_dict(
                    fulfillment_review_escalations_item_data
                )

                fulfillment_review_escalations.append(fulfillment_review_escalations_item)

        _fulfillment_review_reminders = d.pop("fulfillment_review_reminders", UNSET)
        fulfillment_review_reminders: list[OperationWorkflowFulfillmentReviewReminder] | Unset = UNSET
        if _fulfillment_review_reminders is not UNSET:
            fulfillment_review_reminders = []
            for fulfillment_review_reminders_item_data in _fulfillment_review_reminders:
                fulfillment_review_reminders_item = OperationWorkflowFulfillmentReviewReminder.from_dict(
                    fulfillment_review_reminders_item_data
                )

                fulfillment_review_reminders.append(fulfillment_review_reminders_item)

        _review_coordination_changes = d.pop("review_coordination_changes", UNSET)
        review_coordination_changes: list[OperationWorkflowFulfillmentReviewCoordinationChange] | Unset = UNSET
        if _review_coordination_changes is not UNSET:
            review_coordination_changes = []
            for review_coordination_changes_item_data in _review_coordination_changes:
                review_coordination_changes_item = OperationWorkflowFulfillmentReviewCoordinationChange.from_dict(
                    review_coordination_changes_item_data
                )

                review_coordination_changes.append(review_coordination_changes_item)

        _fulfillment_reviews = d.pop("fulfillment_reviews", UNSET)
        fulfillment_reviews: list[OperationWorkflowObligationFulfillmentReview] | Unset = UNSET
        if _fulfillment_reviews is not UNSET:
            fulfillment_reviews = []
            for fulfillment_reviews_item_data in _fulfillment_reviews:
                fulfillment_reviews_item = OperationWorkflowObligationFulfillmentReview.from_dict(
                    fulfillment_reviews_item_data
                )

                fulfillment_reviews.append(fulfillment_reviews_item)

        _reaffirmations = d.pop("reaffirmations", UNSET)
        reaffirmations: list[OperationWorkflowObligationReaffirmation] | Unset = UNSET
        if _reaffirmations is not UNSET:
            reaffirmations = []
            for reaffirmations_item_data in _reaffirmations:
                reaffirmations_item = OperationWorkflowObligationReaffirmation.from_dict(reaffirmations_item_data)

                reaffirmations.append(reaffirmations_item)

        _reopenings = d.pop("reopenings", UNSET)
        reopenings: list[OperationWorkflowObligationReopening] | Unset = UNSET
        if _reopenings is not UNSET:
            reopenings = []
            for reopenings_item_data in _reopenings:
                reopenings_item = OperationWorkflowObligationReopening.from_dict(reopenings_item_data)

                reopenings.append(reopenings_item)

        _fulfillments = d.pop("fulfillments", UNSET)
        fulfillments: list[OperationWorkflowObligationFulfillment] | Unset = UNSET
        if _fulfillments is not UNSET:
            fulfillments = []
            for fulfillments_item_data in _fulfillments:
                fulfillments_item = OperationWorkflowObligationFulfillment.from_dict(fulfillments_item_data)

                fulfillments.append(fulfillments_item)

        _target_bindings = d.pop("target_bindings", UNSET)
        target_bindings: list[OperationWorkflowObligationTargetBinding] | Unset = UNSET
        if _target_bindings is not UNSET:
            target_bindings = []
            for target_bindings_item_data in _target_bindings:
                target_bindings_item = OperationWorkflowObligationTargetBinding.from_dict(target_bindings_item_data)

                target_bindings.append(target_bindings_item)

        _completion = d.pop("completion", UNSET)
        completion: OperationWorkflowReadinessCompletion | Unset
        if isinstance(_completion, Unset):
            completion = UNSET
        else:
            completion = OperationWorkflowReadinessCompletion.from_dict(_completion)

        _follow_up_at = d.pop("follow_up_at", UNSET)
        follow_up_at: datetime.datetime | Unset
        if isinstance(_follow_up_at, Unset):
            follow_up_at = UNSET
        else:
            follow_up_at = datetime.datetime.fromisoformat(_follow_up_at)

        _acknowledged_at = d.pop("acknowledged_at", UNSET)
        acknowledged_at: datetime.datetime | Unset
        if isinstance(_acknowledged_at, Unset):
            acknowledged_at = UNSET
        else:
            acknowledged_at = datetime.datetime.fromisoformat(_acknowledged_at)

        acknowledged_by = d.pop("acknowledged_by", UNSET)

        operation_workflow_readiness_coordination = cls(
            revision=revision,
            status=status,
            owner=owner,
            note=note,
            updated_at=updated_at,
            updated_by=updated_by,
            fulfillment_review_escalations=fulfillment_review_escalations,
            fulfillment_review_reminders=fulfillment_review_reminders,
            review_coordination_changes=review_coordination_changes,
            fulfillment_reviews=fulfillment_reviews,
            reaffirmations=reaffirmations,
            reopenings=reopenings,
            fulfillments=fulfillments,
            target_bindings=target_bindings,
            completion=completion,
            follow_up_at=follow_up_at,
            acknowledged_at=acknowledged_at,
            acknowledged_by=acknowledged_by,
        )

        return operation_workflow_readiness_coordination
