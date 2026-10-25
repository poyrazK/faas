from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.operation_workflow_review_response_source_health_status import (
    OperationWorkflowReviewResponseSourceHealthStatus,
    check_operation_workflow_review_response_source_health_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowReviewResponseSourceHealth")


@_attrs_define
class OperationWorkflowReviewResponseSourceHealth:
    """A scoped source observation for one fixed linked review; unavailable observations never establish review resolution."""

    review_index: int
    """OperationWorkflowReviewResponseSourceHealth.review_index. Zero-based index into the response's fixed reviews
    array."""
    status: OperationWorkflowReviewResponseSourceHealthStatus
    """OperationWorkflowReviewResponseSourceHealth.status. Whether the linked progress, step and declared evidence
    could be observed without availability or staleness issues."""
    checked_at: datetime.datetime
    """OperationWorkflowReviewResponseSourceHealth.checked_at. Latest attempt to observe this source."""
    review_required: bool
    """OperationWorkflowReviewResponseSourceHealth.review_required. Current validity review obligation when the
    step is observable; unavailable sources cannot prove it cleared."""
    blockers: list[str]
    """OperationWorkflowReviewResponseSourceHealth.blockers. Blockers observed in this attempt; consult summary
    remaining blockers for unverified prior observations."""
    explanation: str
    """OperationWorkflowReviewResponseSourceHealth.explanation. Availability explanation without backend error
    details."""
    progress_revision: int | Unset = UNSET
    """OperationWorkflowReviewResponseSourceHealth.progress_revision. Retained linked progress revision; absent if
    progress could not be read."""
    state_revision: int | Unset = UNSET
    """OperationWorkflowReviewResponseSourceHealth.state_revision. Retained application state revision sampled with
    this case; absent if unavailable."""
    last_successful_check_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponseSourceHealth.last_successful_check_at. Most recent available source
    observation, retained through later read failures."""

    def to_dict(self) -> dict[str, Any]:
        review_index = self.review_index

        status: str = self.status

        checked_at = self.checked_at.isoformat()

        review_required = self.review_required

        blockers = self.blockers

        explanation = self.explanation

        progress_revision = self.progress_revision

        state_revision = self.state_revision

        last_successful_check_at: str | Unset = UNSET
        if not isinstance(self.last_successful_check_at, Unset):
            last_successful_check_at = self.last_successful_check_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "review_index": review_index,
                "status": status,
                "checked_at": checked_at,
                "review_required": review_required,
                "blockers": blockers,
                "explanation": explanation,
            }
        )
        if progress_revision is not UNSET:
            field_dict["progress_revision"] = progress_revision
        if state_revision is not UNSET:
            field_dict["state_revision"] = state_revision
        if last_successful_check_at is not UNSET:
            field_dict["last_successful_check_at"] = last_successful_check_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        review_index = d.pop("review_index")

        status = check_operation_workflow_review_response_source_health_status(d.pop("status"))

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        review_required = d.pop("review_required")

        blockers = cast(list[str], d.pop("blockers"))

        explanation = d.pop("explanation")

        progress_revision = d.pop("progress_revision", UNSET)

        state_revision = d.pop("state_revision", UNSET)

        _last_successful_check_at = d.pop("last_successful_check_at", UNSET)
        last_successful_check_at: datetime.datetime | Unset
        if isinstance(_last_successful_check_at, Unset):
            last_successful_check_at = UNSET
        else:
            last_successful_check_at = datetime.datetime.fromisoformat(_last_successful_check_at)

        operation_workflow_review_response_source_health = cls(
            review_index=review_index,
            status=status,
            checked_at=checked_at,
            review_required=review_required,
            blockers=blockers,
            explanation=explanation,
            progress_revision=progress_revision,
            state_revision=state_revision,
            last_successful_check_at=last_successful_check_at,
        )

        return operation_workflow_review_response_source_health
