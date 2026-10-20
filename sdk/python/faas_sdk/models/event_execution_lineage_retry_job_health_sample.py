from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_execution_lineage_retry_job_health_sample_status import (
    EventExecutionLineageRetryJobHealthSampleStatus,
    check_event_execution_lineage_retry_job_health_sample_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventExecutionLineageRetryJobHealthSample")


@_attrs_define
class EventExecutionLineageRetryJobHealthSample:
    """Bounded active-job diagnostic with inactivity, expiry, progress evidence, and original creation guards."""

    request_id: UUID
    status: EventExecutionLineageRetryJobHealthSampleStatus
    processed: int
    selected: int
    remaining: int
    created_at: datetime.datetime
    """Original job incarnation guard for operator controls."""
    last_activity_at: datetime.datetime
    """Authoritative job update timestamp; includes progress and state changes."""
    inactivity_age_seconds: float
    progress_known: bool
    stalled_pending: bool
    """Pending with no job activity for at least 15 minutes; paused jobs never match. Advisory and not proof of
    scheduler failure."""
    retention_risk: bool
    """Active job expires within 24 hours; includes deliberately paused work."""
    retain_until: datetime.datetime
    retention_seconds: float
    last_progress_at: datetime.datetime | Unset = UNSET
    """Last committed case evaluation or skip; absent for unevaluated or legacy jobs."""
    progress_age_seconds: float | Unset = UNSET
    """Age since known last progress or creation before the first evaluation. Absent for legacy evaluated jobs
    without progress evidence."""

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        status: str = self.status

        processed = self.processed

        selected = self.selected

        remaining = self.remaining

        created_at = self.created_at.isoformat()

        last_activity_at = self.last_activity_at.isoformat()

        inactivity_age_seconds = self.inactivity_age_seconds

        progress_known = self.progress_known

        stalled_pending = self.stalled_pending

        retention_risk = self.retention_risk

        retain_until = self.retain_until.isoformat()

        retention_seconds = self.retention_seconds

        last_progress_at: str | Unset = UNSET
        if not isinstance(self.last_progress_at, Unset):
            last_progress_at = self.last_progress_at.isoformat()

        progress_age_seconds = self.progress_age_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_id": request_id,
                "status": status,
                "processed": processed,
                "selected": selected,
                "remaining": remaining,
                "created_at": created_at,
                "last_activity_at": last_activity_at,
                "inactivity_age_seconds": inactivity_age_seconds,
                "progress_known": progress_known,
                "stalled_pending": stalled_pending,
                "retention_risk": retention_risk,
                "retain_until": retain_until,
                "retention_seconds": retention_seconds,
            }
        )
        if last_progress_at is not UNSET:
            field_dict["last_progress_at"] = last_progress_at
        if progress_age_seconds is not UNSET:
            field_dict["progress_age_seconds"] = progress_age_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        status = check_event_execution_lineage_retry_job_health_sample_status(d.pop("status"))

        processed = d.pop("processed")

        selected = d.pop("selected")

        remaining = d.pop("remaining")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        last_activity_at = datetime.datetime.fromisoformat(d.pop("last_activity_at"))

        inactivity_age_seconds = d.pop("inactivity_age_seconds")

        progress_known = d.pop("progress_known")

        stalled_pending = d.pop("stalled_pending")

        retention_risk = d.pop("retention_risk")

        retain_until = datetime.datetime.fromisoformat(d.pop("retain_until"))

        retention_seconds = d.pop("retention_seconds")

        _last_progress_at = d.pop("last_progress_at", UNSET)
        last_progress_at: datetime.datetime | Unset
        if isinstance(_last_progress_at, Unset):
            last_progress_at = UNSET
        else:
            last_progress_at = datetime.datetime.fromisoformat(_last_progress_at)

        progress_age_seconds = d.pop("progress_age_seconds", UNSET)

        event_execution_lineage_retry_job_health_sample = cls(
            request_id=request_id,
            status=status,
            processed=processed,
            selected=selected,
            remaining=remaining,
            created_at=created_at,
            last_activity_at=last_activity_at,
            inactivity_age_seconds=inactivity_age_seconds,
            progress_known=progress_known,
            stalled_pending=stalled_pending,
            retention_risk=retention_risk,
            retain_until=retain_until,
            retention_seconds=retention_seconds,
            last_progress_at=last_progress_at,
            progress_age_seconds=progress_age_seconds,
        )

        return event_execution_lineage_retry_job_health_sample
