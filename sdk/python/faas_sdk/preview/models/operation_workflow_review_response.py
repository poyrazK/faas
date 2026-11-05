from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_review_response_status import (
    OperationWorkflowReviewResponseStatus,
    check_operation_workflow_review_response_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_review_response_change import OperationWorkflowReviewResponseChange
    from ..models.operation_workflow_review_response_decision import OperationWorkflowReviewResponseDecision
    from ..models.operation_workflow_review_response_decision_review import (
        OperationWorkflowReviewResponseDecisionReview,
    )
    from ..models.operation_workflow_review_response_health import OperationWorkflowReviewResponseHealth
    from ..models.operation_workflow_review_response_link import OperationWorkflowReviewResponseLink


T = TypeVar("T", bound="OperationWorkflowReviewResponse")


@_attrs_define
class OperationWorkflowReviewResponse:
    """OperationWorkflowReviewResponse. Human accountability record under the notification coordination revision fence,
    independent of business workflow success.

    """

    owner: str
    """OperationWorkflowReviewResponse.owner. Responsible team label; trimmed control-free UTF-8 limited to 256
    bytes."""
    plan: str
    """OperationWorkflowReviewResponse.plan. Human response plan; nonblank UTF-8 limited to 1024 bytes. Line breaks
    and tabs are allowed."""
    status: OperationWorkflowReviewResponseStatus
    """OperationWorkflowReviewResponse.status. Active ownership or human resolution after a fresh scoped check of
    linked source reviews."""
    reviews: list[OperationWorkflowReviewResponseLink]
    """OperationWorkflowReviewResponse.reviews. Fixed captured review links."""
    created_at: datetime.datetime
    """OperationWorkflowReviewResponse.created_at. Server creation time."""
    created_by: str
    """OperationWorkflowReviewResponse.created_by. Authenticated initial owner-record actor."""
    updated_at: datetime.datetime
    """OperationWorkflowReviewResponse.updated_at. Server latest response update time."""
    updated_by: str
    """OperationWorkflowReviewResponse.updated_by. Authenticated latest response actor."""
    history: list[OperationWorkflowReviewResponseChange]
    """OperationWorkflowReviewResponse.history. Bounded owner, plan, status and check-in change history."""
    history_truncated: bool
    """OperationWorkflowReviewResponse.history_truncated. Whether earlier response changes were pruned."""
    handoff_acceptance_escalation_owner: str | Unset = UNSET
    """OperationWorkflowReviewResponse.handoff_acceptance_escalation_owner. Optional control-free UTF-8 team label
    for future missed acceptance deadlines. Omission preserves policy, empty string clears it; the destination is
    pinned at each actual handoff. Must differ from the parent request owner when explicitly configured."""
    handoff_accept_after_seconds: int | Unset = UNSET
    """OperationWorkflowReviewResponse.handoff_accept_after_seconds. Future handoff acceptance window, defaulting
    to 3600 seconds. Request omission preserves current policy; active acceptance deadlines stay pinned."""
    decision_review_fallback_owner: str | Unset = UNSET
    """OperationWorkflowReviewResponse.decision_review_fallback_owner. Optional distinct fallback team for future
    overdue decision reviews. Request omission preserves policy and empty string clears it; active episodes retain
    their pinned fallback."""
    decision_review_after_seconds: int | Unset = UNSET
    """OperationWorkflowReviewResponse.decision_review_after_seconds. Future decision revalidation window; defaults
    to 86400 seconds. Existing deadlines remain pinned; request omission preserves current policy."""
    decision_review: OperationWorkflowReviewResponseDecisionReview | Unset = UNSET
    """First changed observation for one explicit response decision. Remains latched until a new explicit decision;
    ordinary edits, acknowledgment and subsequent recovery do not clear it."""
    decision: OperationWorkflowReviewResponseDecision | Unset = UNSET
    """Immutable human decision with accountable actor and the reviewed health snapshot. Application execution
    remains application-owned."""
    health: OperationWorkflowReviewResponseHealth | Unset = UNSET
    """Sampled advisory response-plan condition. Clear means no observed blockers or review obligations, not
    business completion. Sampling stops on response resolution."""
    fallback_owner: str | Unset = UNSET
    """OperationWorkflowReviewResponse.fallback_owner. Declared fallback team for overdue response check-ins."""
    check_in_initial_owner: str | Unset = UNSET
    """OperationWorkflowReviewResponse.check_in_initial_owner. Team assigned when this check-in was explicitly
    scheduled; unknown for legacy windows."""
    check_in_owner_changed: bool | Unset = UNSET
    """OperationWorkflowReviewResponse.check_in_owner_changed. Ownership changed during this check-in period;
    prevents automatic takeover even if the owner later reverts."""
    check_in_notified_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponse.check_in_notified_at. One-time overdue notice marker for the current check-
    in; cleared only by a newly scheduled active check-in."""
    check_in_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponse.check_in_at. Required future check-in for active responses; absent when
    resolved. Replaces inbox attention time without changing the original alert response deadline."""
    resolution_checked_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponse.resolution_checked_at. Advisory pre-write source check time for a human
    resolved disposition. Missing or unresolved progress rejects resolution; later regressions do not rewrite
    historical closure."""

    def to_dict(self) -> dict[str, Any]:
        owner = self.owner

        plan = self.plan

        status: str = self.status

        reviews = []
        for reviews_item_data in self.reviews:
            reviews_item = reviews_item_data.to_dict()
            reviews.append(reviews_item)

        created_at = self.created_at.isoformat()

        created_by = self.created_by

        updated_at = self.updated_at.isoformat()

        updated_by = self.updated_by

        history = []
        for history_item_data in self.history:
            history_item = history_item_data.to_dict()
            history.append(history_item)

        history_truncated = self.history_truncated

        handoff_acceptance_escalation_owner = self.handoff_acceptance_escalation_owner

        handoff_accept_after_seconds = self.handoff_accept_after_seconds

        decision_review_fallback_owner = self.decision_review_fallback_owner

        decision_review_after_seconds = self.decision_review_after_seconds

        decision_review: dict[str, Any] | Unset = UNSET
        if not isinstance(self.decision_review, Unset):
            decision_review = self.decision_review.to_dict()

        decision: dict[str, Any] | Unset = UNSET
        if not isinstance(self.decision, Unset):
            decision = self.decision.to_dict()

        health: dict[str, Any] | Unset = UNSET
        if not isinstance(self.health, Unset):
            health = self.health.to_dict()

        fallback_owner = self.fallback_owner

        check_in_initial_owner = self.check_in_initial_owner

        check_in_owner_changed = self.check_in_owner_changed

        check_in_notified_at: str | Unset = UNSET
        if not isinstance(self.check_in_notified_at, Unset):
            check_in_notified_at = self.check_in_notified_at.isoformat()

        check_in_at: str | Unset = UNSET
        if not isinstance(self.check_in_at, Unset):
            check_in_at = self.check_in_at.isoformat()

        resolution_checked_at: str | Unset = UNSET
        if not isinstance(self.resolution_checked_at, Unset):
            resolution_checked_at = self.resolution_checked_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "owner": owner,
                "plan": plan,
                "status": status,
                "reviews": reviews,
                "created_at": created_at,
                "created_by": created_by,
                "updated_at": updated_at,
                "updated_by": updated_by,
                "history": history,
                "history_truncated": history_truncated,
            }
        )
        if handoff_acceptance_escalation_owner is not UNSET:
            field_dict["handoff_acceptance_escalation_owner"] = handoff_acceptance_escalation_owner
        if handoff_accept_after_seconds is not UNSET:
            field_dict["handoff_accept_after_seconds"] = handoff_accept_after_seconds
        if decision_review_fallback_owner is not UNSET:
            field_dict["decision_review_fallback_owner"] = decision_review_fallback_owner
        if decision_review_after_seconds is not UNSET:
            field_dict["decision_review_after_seconds"] = decision_review_after_seconds
        if decision_review is not UNSET:
            field_dict["decision_review"] = decision_review
        if decision is not UNSET:
            field_dict["decision"] = decision
        if health is not UNSET:
            field_dict["health"] = health
        if fallback_owner is not UNSET:
            field_dict["fallback_owner"] = fallback_owner
        if check_in_initial_owner is not UNSET:
            field_dict["check_in_initial_owner"] = check_in_initial_owner
        if check_in_owner_changed is not UNSET:
            field_dict["check_in_owner_changed"] = check_in_owner_changed
        if check_in_notified_at is not UNSET:
            field_dict["check_in_notified_at"] = check_in_notified_at
        if check_in_at is not UNSET:
            field_dict["check_in_at"] = check_in_at
        if resolution_checked_at is not UNSET:
            field_dict["resolution_checked_at"] = resolution_checked_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_review_response_change import OperationWorkflowReviewResponseChange
        from ..models.operation_workflow_review_response_decision import OperationWorkflowReviewResponseDecision
        from ..models.operation_workflow_review_response_decision_review import (
            OperationWorkflowReviewResponseDecisionReview,
        )
        from ..models.operation_workflow_review_response_health import OperationWorkflowReviewResponseHealth
        from ..models.operation_workflow_review_response_link import OperationWorkflowReviewResponseLink

        d = dict(src_dict)
        owner = d.pop("owner")

        plan = d.pop("plan")

        status = check_operation_workflow_review_response_status(d.pop("status"))

        reviews = []
        _reviews = d.pop("reviews")
        for reviews_item_data in _reviews:
            reviews_item = OperationWorkflowReviewResponseLink.from_dict(reviews_item_data)

            reviews.append(reviews_item)

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        created_by = d.pop("created_by")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        updated_by = d.pop("updated_by")

        history = []
        _history = d.pop("history")
        for history_item_data in _history:
            history_item = OperationWorkflowReviewResponseChange.from_dict(history_item_data)

            history.append(history_item)

        history_truncated = d.pop("history_truncated")

        handoff_acceptance_escalation_owner = d.pop("handoff_acceptance_escalation_owner", UNSET)

        handoff_accept_after_seconds = d.pop("handoff_accept_after_seconds", UNSET)

        decision_review_fallback_owner = d.pop("decision_review_fallback_owner", UNSET)

        decision_review_after_seconds = d.pop("decision_review_after_seconds", UNSET)

        _decision_review = d.pop("decision_review", UNSET)
        decision_review: OperationWorkflowReviewResponseDecisionReview | Unset
        if isinstance(_decision_review, Unset):
            decision_review = UNSET
        else:
            decision_review = OperationWorkflowReviewResponseDecisionReview.from_dict(_decision_review)

        _decision = d.pop("decision", UNSET)
        decision: OperationWorkflowReviewResponseDecision | Unset
        if isinstance(_decision, Unset):
            decision = UNSET
        else:
            decision = OperationWorkflowReviewResponseDecision.from_dict(_decision)

        _health = d.pop("health", UNSET)
        health: OperationWorkflowReviewResponseHealth | Unset
        if isinstance(_health, Unset):
            health = UNSET
        else:
            health = OperationWorkflowReviewResponseHealth.from_dict(_health)

        fallback_owner = d.pop("fallback_owner", UNSET)

        check_in_initial_owner = d.pop("check_in_initial_owner", UNSET)

        check_in_owner_changed = d.pop("check_in_owner_changed", UNSET)

        _check_in_notified_at = d.pop("check_in_notified_at", UNSET)
        check_in_notified_at: datetime.datetime | Unset
        if isinstance(_check_in_notified_at, Unset):
            check_in_notified_at = UNSET
        else:
            check_in_notified_at = datetime.datetime.fromisoformat(_check_in_notified_at)

        _check_in_at = d.pop("check_in_at", UNSET)
        check_in_at: datetime.datetime | Unset
        if isinstance(_check_in_at, Unset):
            check_in_at = UNSET
        else:
            check_in_at = datetime.datetime.fromisoformat(_check_in_at)

        _resolution_checked_at = d.pop("resolution_checked_at", UNSET)
        resolution_checked_at: datetime.datetime | Unset
        if isinstance(_resolution_checked_at, Unset):
            resolution_checked_at = UNSET
        else:
            resolution_checked_at = datetime.datetime.fromisoformat(_resolution_checked_at)

        operation_workflow_review_response = cls(
            owner=owner,
            plan=plan,
            status=status,
            reviews=reviews,
            created_at=created_at,
            created_by=created_by,
            updated_at=updated_at,
            updated_by=updated_by,
            history=history,
            history_truncated=history_truncated,
            handoff_acceptance_escalation_owner=handoff_acceptance_escalation_owner,
            handoff_accept_after_seconds=handoff_accept_after_seconds,
            decision_review_fallback_owner=decision_review_fallback_owner,
            decision_review_after_seconds=decision_review_after_seconds,
            decision_review=decision_review,
            decision=decision,
            health=health,
            fallback_owner=fallback_owner,
            check_in_initial_owner=check_in_initial_owner,
            check_in_owner_changed=check_in_owner_changed,
            check_in_notified_at=check_in_notified_at,
            check_in_at=check_in_at,
            resolution_checked_at=resolution_checked_at,
        )

        return operation_workflow_review_response
