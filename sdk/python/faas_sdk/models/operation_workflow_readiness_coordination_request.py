from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_coordination_request_status import (
    OperationWorkflowReadinessCoordinationRequestStatus,
    check_operation_workflow_readiness_coordination_request_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_fulfillment_review_coordination_request import (
        OperationWorkflowFulfillmentReviewCoordinationRequest,
    )
    from ..models.operation_workflow_obligation_fulfillment_request import OperationWorkflowObligationFulfillmentRequest
    from ..models.operation_workflow_obligation_reaffirmation_request import (
        OperationWorkflowObligationReaffirmationRequest,
    )
    from ..models.operation_workflow_obligation_reopening_request import OperationWorkflowObligationReopeningRequest
    from ..models.operation_workflow_obligation_target_request import OperationWorkflowObligationTargetRequest
    from ..models.operation_workflow_readiness_completion_request import OperationWorkflowReadinessCompletionRequest
    from ..models.operation_workflow_review_response_request import OperationWorkflowReviewResponseRequest


T = TypeVar("T", bound="OperationWorkflowReadinessCoordinationRequest")


@_attrs_define
class OperationWorkflowReadinessCoordinationRequest:
    """OperationWorkflowReadinessCoordinationRequest. Shared inbox coordination with optimistic revision checks; omission
    of a follow-up time clears it.

    """

    revision: int
    """OperationWorkflowReadinessCoordinationRequest.revision. Shared coordination value; does not alter
    application evidence."""
    status: OperationWorkflowReadinessCoordinationRequestStatus
    """OperationWorkflowReadinessCoordinationRequest.status. Shared coordination value; does not alter application
    evidence."""
    owner: str
    """OperationWorkflowReadinessCoordinationRequest.owner. Shared coordination value; does not alter application
    evidence."""
    note: str
    """OperationWorkflowReadinessCoordinationRequest.note. Shared coordination value; does not alter application
    evidence."""
    review_coordination: OperationWorkflowFulfillmentReviewCoordinationRequest | Unset = UNSET
    """Assign and schedule one exact pending fulfillment-review cycle with a reason. Source revision and review
    fingerprint fence stale edits. Preserve source status, owner, note and deadline; do not combine with other
    actions. Configure an optional escalation team/grace period; reassignment after an overdue reminder cancels
    escalation for that deadline, including reverted routing. Removing the deadline disables escalation when policy
    fields are omitted. Empty owner explicitly unassigns; omitted due_at clears the review deadline. Original
    fulfillment remains intact."""
    reaffirmation: OperationWorkflowObligationReaffirmationRequest | Unset = UNSET
    """Explicitly reaffirm a flagged latest fulfillment without reopening work. Preserve source status, owner, note
    and deadline and do not combine with other actions. Source revision, binding, fulfillment time and review
    fingerprint fence stale decisions. Evidence must be a full application state report for the exact case under the
    fresh current contract and be published after review was required; current retained evidence is rechecked
    independently before commit."""
    reopening: OperationWorkflowObligationReopeningRequest | Unset = UNSET
    """Explicitly reopen the latest fulfillment for an exact bound case and current source cycle, with a reason.
    Preserve source status, owner, note and deadline; cannot combine with other action edits. Retired sources,
    superseded cycles and changed/cleared bindings cannot be reopened. Reopening does not execute an application
    transition."""
    fulfillment: OperationWorkflowObligationFulfillmentRequest | Unset = UNSET
    """Explicit fulfillment of one current bound case/cycle. Must preserve source status, owner, note and deadline
    and cannot combine with target binding, response or shared completion edits. The full binding fences replacement
    or rebinding. Evidence must be a retained application report of the exact target transition, published after
    binding; current readiness is insufficient."""
    target_binding: OperationWorkflowObligationTargetRequest | Unset = UNSET
    """Bind or clear one active free-form plan obligation for an exact case. Clear requires no transition; bind
    requires a fresh declared current-state transition. Requires the current coordination revision and unchanged
    status, owner, note and deadline; cannot combine with completion or review-response edits. Customer cases omit
    tenant IDs; operators specify the exact customer. Source obligation identity is rechecked under its write lock;
    evidence observation and persistence are independent."""
    review_response: OperationWorkflowReviewResponseRequest | Unset = UNSET
    """OperationWorkflowReviewResponseRequest. Explicit follow-through on an original critical or overdue business-
    impact alert. Saving acknowledges the alert and assigns queue attention to the response owner."""
    completion: OperationWorkflowReadinessCompletionRequest | Unset = UNSET
    """An asserted coordination outcome. Generic completion requires a note and resolved requires retained
    evidence; playbook business completion captures its own current evidence. Closing does not prove business
    resolution."""
    follow_up_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReadinessCoordinationRequest.follow_up_at. Shared coordination value; does not alter
    application evidence."""

    def to_dict(self) -> dict[str, Any]:
        revision = self.revision

        status: str = self.status

        owner = self.owner

        note = self.note

        review_coordination: dict[str, Any] | Unset = UNSET
        if not isinstance(self.review_coordination, Unset):
            review_coordination = self.review_coordination.to_dict()

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

        review_response: dict[str, Any] | Unset = UNSET
        if not isinstance(self.review_response, Unset):
            review_response = self.review_response.to_dict()

        completion: dict[str, Any] | Unset = UNSET
        if not isinstance(self.completion, Unset):
            completion = self.completion.to_dict()

        follow_up_at: str | Unset = UNSET
        if not isinstance(self.follow_up_at, Unset):
            follow_up_at = self.follow_up_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "revision": revision,
                "status": status,
                "owner": owner,
                "note": note,
            }
        )
        if review_coordination is not UNSET:
            field_dict["review_coordination"] = review_coordination
        if reaffirmation is not UNSET:
            field_dict["reaffirmation"] = reaffirmation
        if reopening is not UNSET:
            field_dict["reopening"] = reopening
        if fulfillment is not UNSET:
            field_dict["fulfillment"] = fulfillment
        if target_binding is not UNSET:
            field_dict["target_binding"] = target_binding
        if review_response is not UNSET:
            field_dict["review_response"] = review_response
        if completion is not UNSET:
            field_dict["completion"] = completion
        if follow_up_at is not UNSET:
            field_dict["follow_up_at"] = follow_up_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_fulfillment_review_coordination_request import (
            OperationWorkflowFulfillmentReviewCoordinationRequest,
        )
        from ..models.operation_workflow_obligation_fulfillment_request import (
            OperationWorkflowObligationFulfillmentRequest,
        )
        from ..models.operation_workflow_obligation_reaffirmation_request import (
            OperationWorkflowObligationReaffirmationRequest,
        )
        from ..models.operation_workflow_obligation_reopening_request import OperationWorkflowObligationReopeningRequest
        from ..models.operation_workflow_obligation_target_request import OperationWorkflowObligationTargetRequest
        from ..models.operation_workflow_readiness_completion_request import OperationWorkflowReadinessCompletionRequest
        from ..models.operation_workflow_review_response_request import OperationWorkflowReviewResponseRequest

        d = dict(src_dict)
        revision = d.pop("revision")

        status = check_operation_workflow_readiness_coordination_request_status(d.pop("status"))

        owner = d.pop("owner")

        note = d.pop("note")

        _review_coordination = d.pop("review_coordination", UNSET)
        review_coordination: OperationWorkflowFulfillmentReviewCoordinationRequest | Unset
        if isinstance(_review_coordination, Unset):
            review_coordination = UNSET
        else:
            review_coordination = OperationWorkflowFulfillmentReviewCoordinationRequest.from_dict(_review_coordination)

        _reaffirmation = d.pop("reaffirmation", UNSET)
        reaffirmation: OperationWorkflowObligationReaffirmationRequest | Unset
        if isinstance(_reaffirmation, Unset):
            reaffirmation = UNSET
        else:
            reaffirmation = OperationWorkflowObligationReaffirmationRequest.from_dict(_reaffirmation)

        _reopening = d.pop("reopening", UNSET)
        reopening: OperationWorkflowObligationReopeningRequest | Unset
        if isinstance(_reopening, Unset):
            reopening = UNSET
        else:
            reopening = OperationWorkflowObligationReopeningRequest.from_dict(_reopening)

        _fulfillment = d.pop("fulfillment", UNSET)
        fulfillment: OperationWorkflowObligationFulfillmentRequest | Unset
        if isinstance(_fulfillment, Unset):
            fulfillment = UNSET
        else:
            fulfillment = OperationWorkflowObligationFulfillmentRequest.from_dict(_fulfillment)

        _target_binding = d.pop("target_binding", UNSET)
        target_binding: OperationWorkflowObligationTargetRequest | Unset
        if isinstance(_target_binding, Unset):
            target_binding = UNSET
        else:
            target_binding = OperationWorkflowObligationTargetRequest.from_dict(_target_binding)

        _review_response = d.pop("review_response", UNSET)
        review_response: OperationWorkflowReviewResponseRequest | Unset
        if isinstance(_review_response, Unset):
            review_response = UNSET
        else:
            review_response = OperationWorkflowReviewResponseRequest.from_dict(_review_response)

        _completion = d.pop("completion", UNSET)
        completion: OperationWorkflowReadinessCompletionRequest | Unset
        if isinstance(_completion, Unset):
            completion = UNSET
        else:
            completion = OperationWorkflowReadinessCompletionRequest.from_dict(_completion)

        _follow_up_at = d.pop("follow_up_at", UNSET)
        follow_up_at: datetime.datetime | Unset
        if isinstance(_follow_up_at, Unset):
            follow_up_at = UNSET
        else:
            follow_up_at = datetime.datetime.fromisoformat(_follow_up_at)

        operation_workflow_readiness_coordination_request = cls(
            revision=revision,
            status=status,
            owner=owner,
            note=note,
            review_coordination=review_coordination,
            reaffirmation=reaffirmation,
            reopening=reopening,
            fulfillment=fulfillment,
            target_binding=target_binding,
            review_response=review_response,
            completion=completion,
            follow_up_at=follow_up_at,
        )

        return operation_workflow_readiness_coordination_request
