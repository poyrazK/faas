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
        if check_in_at is not UNSET:
            field_dict["check_in_at"] = check_in_at
        if resolution_checked_at is not UNSET:
            field_dict["resolution_checked_at"] = resolution_checked_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_review_response_change import OperationWorkflowReviewResponseChange
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
            check_in_at=check_in_at,
            resolution_checked_at=resolution_checked_at,
        )

        return operation_workflow_review_response
