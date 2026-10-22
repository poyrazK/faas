from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_execution_lineage_retry_job_summary_status import (
    EventExecutionLineageRetryJobSummaryStatus,
    check_event_execution_lineage_retry_job_summary_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_execution_lineage_retry_job_health_sample import EventExecutionLineageRetryJobHealthSample


T = TypeVar("T", bound="EventExecutionLineageRetryJobSummary")


@_attrs_define
class EventExecutionLineageRetryJobSummary:
    """Retained bulk retry job identity, creation guards, lifecycle status, and progress summary."""

    request_id: UUID
    subscription_id: UUID
    app_id: UUID
    status: EventExecutionLineageRetryJobSummaryStatus
    selected: int
    processed: int
    """Cases evaluated or skipped; cancelled items are not counted."""
    created_at: datetime.datetime
    updated_at: datetime.datetime
    retain_until: datetime.datetime
    last_progress_at: datetime.datetime | Unset = UNSET
    """Last known committed evaluation or skip; absent for unevaluated or legacy jobs."""
    attention: EventExecutionLineageRetryJobHealthSample | Unset = UNSET
    """Bounded active-job diagnostic with inactivity, expiry, progress evidence, and original creation guards."""

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        subscription_id = str(self.subscription_id)

        app_id = str(self.app_id)

        status: str = self.status

        selected = self.selected

        processed = self.processed

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        retain_until = self.retain_until.isoformat()

        last_progress_at: str | Unset = UNSET
        if not isinstance(self.last_progress_at, Unset):
            last_progress_at = self.last_progress_at.isoformat()

        attention: dict[str, Any] | Unset = UNSET
        if not isinstance(self.attention, Unset):
            attention = self.attention.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_id": request_id,
                "subscription_id": subscription_id,
                "app_id": app_id,
                "status": status,
                "selected": selected,
                "processed": processed,
                "created_at": created_at,
                "updated_at": updated_at,
                "retain_until": retain_until,
            }
        )
        if last_progress_at is not UNSET:
            field_dict["last_progress_at"] = last_progress_at
        if attention is not UNSET:
            field_dict["attention"] = attention

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_execution_lineage_retry_job_health_sample import EventExecutionLineageRetryJobHealthSample

        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        subscription_id = UUID(d.pop("subscription_id"))

        app_id = UUID(d.pop("app_id"))

        status = check_event_execution_lineage_retry_job_summary_status(d.pop("status"))

        selected = d.pop("selected")

        processed = d.pop("processed")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        retain_until = datetime.datetime.fromisoformat(d.pop("retain_until"))

        _last_progress_at = d.pop("last_progress_at", UNSET)
        last_progress_at: datetime.datetime | Unset
        if isinstance(_last_progress_at, Unset):
            last_progress_at = UNSET
        else:
            last_progress_at = datetime.datetime.fromisoformat(_last_progress_at)

        _attention = d.pop("attention", UNSET)
        attention: EventExecutionLineageRetryJobHealthSample | Unset
        if isinstance(_attention, Unset):
            attention = UNSET
        else:
            attention = EventExecutionLineageRetryJobHealthSample.from_dict(_attention)

        event_execution_lineage_retry_job_summary = cls(
            request_id=request_id,
            subscription_id=subscription_id,
            app_id=app_id,
            status=status,
            selected=selected,
            processed=processed,
            created_at=created_at,
            updated_at=updated_at,
            retain_until=retain_until,
            last_progress_at=last_progress_at,
            attention=attention,
        )

        return event_execution_lineage_retry_job_summary
