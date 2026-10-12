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
        from ..models.operation_workflow_review_response_link_request import OperationWorkflowReviewResponseLinkRequest

        d = dict(src_dict)
        owner = d.pop("owner")

        plan = d.pop("plan")

        status = check_operation_workflow_review_response_request_status(d.pop("status"))

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
            decision=decision,
            fallback_owner=fallback_owner,
            check_in_at=check_in_at,
            reviews=reviews,
        )

        return operation_workflow_review_response_request
