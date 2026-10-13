from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_review_response_request_status import (
    OperationWorkflowReviewResponseRequestStatus,
    check_operation_workflow_review_response_request_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_review_response_decision_request import (
        OperationWorkflowReviewResponseDecisionRequest,
    )
    from ..models.operation_workflow_review_response_handoff_acceptance_request import (
        OperationWorkflowReviewResponseHandoffAcceptanceRequest,
    )
    from ..models.operation_workflow_review_response_handoff_decline_request import (
        OperationWorkflowReviewResponseHandoffDeclineRequest,
    )
    from ..models.operation_workflow_review_response_handoff_reassignment_request import (
        OperationWorkflowReviewResponseHandoffReassignmentRequest,
    )
    from ..models.operation_workflow_review_response_link_request import OperationWorkflowReviewResponseLinkRequest


T = TypeVar("T", bound="OperationWorkflowReviewResponseRequest")


@_attrs_define
class OperationWorkflowReviewResponseRequest:
    """OperationWorkflowReviewResponseRequest. Explicit follow-through on an original critical or overdue business-impact
    alert. Saving acknowledges the alert and assigns queue attention to the response owner.

    """

    owner: str
    """OperationWorkflowReviewResponseRequest.owner. Responsible team label; trimmed control-free UTF-8 limited to
    256 bytes."""
    plan: str
    """OperationWorkflowReviewResponseRequest.plan. Human response plan; nonblank UTF-8 limited to 1024 bytes. Line
    breaks and tabs are allowed."""
    status: OperationWorkflowReviewResponseRequestStatus
    """OperationWorkflowReviewResponseRequest.status. Active ownership or human resolution after a fresh scoped
    check of linked source reviews."""
    handoff_acceptance_escalation_owner: str | Unset = UNSET
    """OperationWorkflowReviewResponseRequest.handoff_acceptance_escalation_owner. Optional control-free UTF-8 team
    label for future missed acceptance deadlines. Omission preserves policy, empty string clears it; the destination
    is pinned at each actual handoff. Must differ from the parent request owner when explicitly configured."""
    handoff_reassignment: OperationWorkflowReviewResponseHandoffReassignmentRequest | Unset = UNSET
    """Reassign a currently declined handoff to a different parent response owner. Preserve plan, check-in and
    review deadline; start a new acceptance clock. Cannot combine with a decision, acceptance or decline."""
    handoff_decline: OperationWorkflowReviewResponseHandoffDeclineRequest | Unset = UNSET
    """Decline one current unaccepted handoff without a decision or acceptance. Retain its owner, plan and exact
    check-in, even if already overdue; deadlines do not restart."""
    handoff_accept_after_seconds: int | Unset = UNSET
    """OperationWorkflowReviewResponseRequest.handoff_accept_after_seconds. Future handoff acceptance window,
    defaulting to 3600 seconds. Request omission preserves current policy; active acceptance deadlines stay pinned.
   """
    handoff_acceptance: OperationWorkflowReviewResponseHandoffAcceptanceRequest | Unset = UNSET
    """Accept the current reassigned review for its responsible team. Parent response must remain active with the
    handed-off owner and a future check-in, without an accompanying decision."""
    decision_review_fallback_owner: str | Unset = UNSET
    """OperationWorkflowReviewResponseRequest.decision_review_fallback_owner. Optional distinct fallback team for
    future overdue decision reviews. Request omission preserves policy and empty string clears it; active episodes
    retain their pinned fallback."""
    decision_review_after_seconds: int | Unset = UNSET
    """OperationWorkflowReviewResponseRequest.decision_review_after_seconds. Future decision revalidation window;
    defaults to 86400 seconds. Existing deadlines remain pinned; request omission preserves current policy."""
    decision: OperationWorkflowReviewResponseDecisionRequest | Unset = UNSET
    """Explicit advisory continue, revise or pause decision on an existing active response. Saving re-observes
    scoped sources and checks the reviewed health digest again under the notification lock."""
    fallback_owner: str | Unset = UNSET
    """OperationWorkflowReviewResponseRequest.fallback_owner. Optional distinct team for a missed check-in; omit to
    preserve existing policy, or send empty to clear it. Explicit ownership changes protect the current check-in
    from automatic takeover."""
    check_in_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponseRequest.check_in_at. Required future check-in for active responses; absent
    when resolved. Replaces inbox attention time without changing the original alert response deadline."""
    reviews: list[OperationWorkflowReviewResponseLinkRequest] | Unset = UNSET
    """OperationWorkflowReviewResponseRequest.reviews. Required nonempty link set on first creation. Omit on later
    edits to preserve it; supplied sets must exactly match existing links. Links cannot be dropped to manufacture
    resolution."""

    def to_dict(self) -> dict[str, Any]:
        owner = self.owner

        plan = self.plan

        status: str = self.status

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

        check_in_at: str | Unset = UNSET
        if not isinstance(self.check_in_at, Unset):
            check_in_at = self.check_in_at.isoformat()

        reviews: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.reviews, Unset):
            reviews = []
            for reviews_item_data in self.reviews:
                reviews_item = reviews_item_data.to_dict()
                reviews.append(reviews_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "owner": owner,
                "plan": plan,
                "status": status,
            }
        )
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
        if check_in_at is not UNSET:
            field_dict["check_in_at"] = check_in_at
        if reviews is not UNSET:
            field_dict["reviews"] = reviews

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_review_response_decision_request import (
            OperationWorkflowReviewResponseDecisionRequest,
        )
        from ..models.operation_workflow_review_response_handoff_acceptance_request import (
            OperationWorkflowReviewResponseHandoffAcceptanceRequest,
        )
        from ..models.operation_workflow_review_response_handoff_decline_request import (
            OperationWorkflowReviewResponseHandoffDeclineRequest,
        )
        from ..models.operation_workflow_review_response_handoff_reassignment_request import (
            OperationWorkflowReviewResponseHandoffReassignmentRequest,
        )
        from ..models.operation_workflow_review_response_link_request import OperationWorkflowReviewResponseLinkRequest

        d = dict(src_dict)
        owner = d.pop("owner")

        plan = d.pop("plan")

        status = check_operation_workflow_review_response_request_status(d.pop("status"))

        handoff_acceptance_escalation_owner = d.pop("handoff_acceptance_escalation_owner", UNSET)

        _handoff_reassignment = d.pop("handoff_reassignment", UNSET)
        handoff_reassignment: OperationWorkflowReviewResponseHandoffReassignmentRequest | Unset
        if isinstance(_handoff_reassignment, Unset):
            handoff_reassignment = UNSET
        else:
            handoff_reassignment = OperationWorkflowReviewResponseHandoffReassignmentRequest.from_dict(
                _handoff_reassignment
            )

        _handoff_decline = d.pop("handoff_decline", UNSET)
        handoff_decline: OperationWorkflowReviewResponseHandoffDeclineRequest | Unset
        if isinstance(_handoff_decline, Unset):
            handoff_decline = UNSET
        else:
            handoff_decline = OperationWorkflowReviewResponseHandoffDeclineRequest.from_dict(_handoff_decline)

        handoff_accept_after_seconds = d.pop("handoff_accept_after_seconds", UNSET)

        _handoff_acceptance = d.pop("handoff_acceptance", UNSET)
        handoff_acceptance: OperationWorkflowReviewResponseHandoffAcceptanceRequest | Unset
        if isinstance(_handoff_acceptance, Unset):
            handoff_acceptance = UNSET
        else:
            handoff_acceptance = OperationWorkflowReviewResponseHandoffAcceptanceRequest.from_dict(_handoff_acceptance)

        decision_review_fallback_owner = d.pop("decision_review_fallback_owner", UNSET)

        decision_review_after_seconds = d.pop("decision_review_after_seconds", UNSET)

        _decision = d.pop("decision", UNSET)
        decision: OperationWorkflowReviewResponseDecisionRequest | Unset
        if isinstance(_decision, Unset):
            decision = UNSET
        else:
            decision = OperationWorkflowReviewResponseDecisionRequest.from_dict(_decision)

        fallback_owner = d.pop("fallback_owner", UNSET)

        _check_in_at = d.pop("check_in_at", UNSET)
        check_in_at: datetime.datetime | Unset
        if isinstance(_check_in_at, Unset):
            check_in_at = UNSET
        else:
            check_in_at = datetime.datetime.fromisoformat(_check_in_at)

        _reviews = d.pop("reviews", UNSET)
        reviews: list[OperationWorkflowReviewResponseLinkRequest] | Unset = UNSET
        if _reviews is not UNSET:
            reviews = []
            for reviews_item_data in _reviews:
                reviews_item = OperationWorkflowReviewResponseLinkRequest.from_dict(reviews_item_data)

                reviews.append(reviews_item)

        operation_workflow_review_response_request = cls(
            owner=owner,
            plan=plan,
            status=status,
            handoff_acceptance_escalation_owner=handoff_acceptance_escalation_owner,
            handoff_reassignment=handoff_reassignment,
            handoff_decline=handoff_decline,
            handoff_accept_after_seconds=handoff_accept_after_seconds,
            handoff_acceptance=handoff_acceptance,
            decision_review_fallback_owner=decision_review_fallback_owner,
            decision_review_after_seconds=decision_review_after_seconds,
            decision=decision,
            fallback_owner=fallback_owner,
            check_in_at=check_in_at,
            reviews=reviews,
        )

        return operation_workflow_review_response_request
