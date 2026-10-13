from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.operation_workflow_review_response_health_status import (
    OperationWorkflowReviewResponseHealthStatus,
    check_operation_workflow_review_response_health_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_review_response_source_health import OperationWorkflowReviewResponseSourceHealth


T = TypeVar("T", bound="OperationWorkflowReviewResponseHealth")


@_attrs_define
class OperationWorkflowReviewResponseHealth:
    """Sampled advisory response-plan condition. Clear means no observed blockers or review obligations, not business
    completion. Sampling stops on response resolution.

    """

    status: OperationWorkflowReviewResponseHealthStatus
    """OperationWorkflowReviewResponseHealth.status. Unavailable takes precedence over blocked, pending review and
    clear."""
    checked_at: datetime.datetime
    """OperationWorkflowReviewResponseHealth.checked_at. Latest attempted sampling time; consult individual sources
    for freshness."""
    remaining_blockers: list[str]
    """OperationWorkflowReviewResponseHealth.remaining_blockers. Observed blockers plus unverified disappearances
    retained until positive clearance."""
    recovered_blockers: list[str]
    """OperationWorkflowReviewResponseHealth.recovered_blockers. Most recent positively verified recoveries,
    excluding blockers subsequently observed again."""
    sources: list[OperationWorkflowReviewResponseSourceHealth]
    """OperationWorkflowReviewResponseHealth.sources. Condition and freshness for each fixed linked review."""
    fingerprint: str | Unset = UNSET
    """OperationWorkflowReviewResponseHealth.fingerprint. Stable digest of source revisions and current observed
    condition; polling timestamps and recovery history are excluded."""
    last_complete_check_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponseHealth.last_complete_check_at. Latest sample whose scoped reads all
    completed, including observations of missing or retired progress."""
    recovered_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponseHealth.recovered_at. Time of the latest positive recovery sample."""

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        checked_at = self.checked_at.isoformat()

        remaining_blockers = self.remaining_blockers

        recovered_blockers = self.recovered_blockers

        sources = []
        for sources_item_data in self.sources:
            sources_item = sources_item_data.to_dict()
            sources.append(sources_item)

        fingerprint = self.fingerprint

        last_complete_check_at: str | Unset = UNSET
        if not isinstance(self.last_complete_check_at, Unset):
            last_complete_check_at = self.last_complete_check_at.isoformat()

        recovered_at: str | Unset = UNSET
        if not isinstance(self.recovered_at, Unset):
            recovered_at = self.recovered_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "status": status,
                "checked_at": checked_at,
                "remaining_blockers": remaining_blockers,
                "recovered_blockers": recovered_blockers,
                "sources": sources,
            }
        )
        if fingerprint is not UNSET:
            field_dict["fingerprint"] = fingerprint
        if last_complete_check_at is not UNSET:
            field_dict["last_complete_check_at"] = last_complete_check_at
        if recovered_at is not UNSET:
            field_dict["recovered_at"] = recovered_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_review_response_source_health import (
            OperationWorkflowReviewResponseSourceHealth,
        )

        d = dict(src_dict)
        status = check_operation_workflow_review_response_health_status(d.pop("status"))

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        remaining_blockers = cast(list[str], d.pop("remaining_blockers"))

        recovered_blockers = cast(list[str], d.pop("recovered_blockers"))

        sources = []
        _sources = d.pop("sources")
        for sources_item_data in _sources:
            sources_item = OperationWorkflowReviewResponseSourceHealth.from_dict(sources_item_data)

            sources.append(sources_item)

        fingerprint = d.pop("fingerprint", UNSET)

        _last_complete_check_at = d.pop("last_complete_check_at", UNSET)
        last_complete_check_at: datetime.datetime | Unset
        if isinstance(_last_complete_check_at, Unset):
            last_complete_check_at = UNSET
        else:
            last_complete_check_at = datetime.datetime.fromisoformat(_last_complete_check_at)

        _recovered_at = d.pop("recovered_at", UNSET)
        recovered_at: datetime.datetime | Unset
        if isinstance(_recovered_at, Unset):
            recovered_at = UNSET
        else:
            recovered_at = datetime.datetime.fromisoformat(_recovered_at)

        operation_workflow_review_response_health = cls(
            status=status,
            checked_at=checked_at,
            remaining_blockers=remaining_blockers,
            recovered_blockers=recovered_blockers,
            sources=sources,
            fingerprint=fingerprint,
            last_complete_check_at=last_complete_check_at,
            recovered_at=recovered_at,
        )

        return operation_workflow_review_response_health
