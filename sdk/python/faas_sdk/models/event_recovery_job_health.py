from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_recovery_job_health_mode import EventRecoveryJobHealthMode, check_event_recovery_job_health_mode
from ..models.event_recovery_job_health_state import EventRecoveryJobHealthState, check_event_recovery_job_health_state
from ..models.event_recovery_job_health_status import (
    EventRecoveryJobHealthStatus,
    check_event_recovery_job_health_status,
)
from ..models.event_recovery_job_health_wait_reason import (
    EventRecoveryJobHealthWaitReason,
    check_event_recovery_job_health_wait_reason,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_recovery_capacity_wait import EventRecoveryCapacityWait


T = TypeVar("T", bound="EventRecoveryJobHealth")


@_attrs_define
class EventRecoveryJobHealth:
    job_id: UUID
    mode: EventRecoveryJobHealthMode
    state: EventRecoveryJobHealthState
    status: EventRecoveryJobHealthStatus
    pending_count: int
    rate_per_second: int
    progress_age_seconds: float
    """Age of last tracked progress; falls back to job creation when progress_known is false. This fallback does
    not reconstruct historical progress."""
    progress_known: bool
    next_attempt_at: datetime.datetime
    eligible_at: datetime.datetime
    """Later of the scheduled attempt and the end of a spent rate window. Eligibility is ignored while paused."""
    overdue_seconds: float
    expires_at: datetime.datetime
    expiring: bool
    """Pending work exists and expiry is within one hour or already overdue. Paused jobs retain this signal
    separately."""
    capacity_wait: EventRecoveryCapacityWait | Unset = UNSET
    last_progress_at: datetime.datetime | Unset = UNSET
    """Last committed item admission or skip; control changes and capacity deferrals do not advance this timestamp.
    Omitted when no tracked progress exists."""
    wait_reason: EventRecoveryJobHealthWaitReason | Unset = UNSET
    """Most recently recorded admission wait; cleared on tracked progress."""

    def to_dict(self) -> dict[str, Any]:
        job_id = str(self.job_id)

        mode: str = self.mode

        state: str = self.state

        status: str = self.status

        pending_count = self.pending_count

        rate_per_second = self.rate_per_second

        progress_age_seconds = self.progress_age_seconds

        progress_known = self.progress_known

        next_attempt_at = self.next_attempt_at.isoformat()

        eligible_at = self.eligible_at.isoformat()

        overdue_seconds = self.overdue_seconds

        expires_at = self.expires_at.isoformat()

        expiring = self.expiring

        capacity_wait: dict[str, Any] | Unset = UNSET
        if not isinstance(self.capacity_wait, Unset):
            capacity_wait = self.capacity_wait.to_dict()

        last_progress_at: str | Unset = UNSET
        if not isinstance(self.last_progress_at, Unset):
            last_progress_at = self.last_progress_at.isoformat()

        wait_reason: str | Unset = UNSET
        if not isinstance(self.wait_reason, Unset):
            wait_reason = self.wait_reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "job_id": job_id,
                "mode": mode,
                "state": state,
                "status": status,
                "pending_count": pending_count,
                "rate_per_second": rate_per_second,
                "progress_age_seconds": progress_age_seconds,
                "progress_known": progress_known,
                "next_attempt_at": next_attempt_at,
                "eligible_at": eligible_at,
                "overdue_seconds": overdue_seconds,
                "expires_at": expires_at,
                "expiring": expiring,
            }
        )
        if capacity_wait is not UNSET:
            field_dict["capacity_wait"] = capacity_wait
        if last_progress_at is not UNSET:
            field_dict["last_progress_at"] = last_progress_at
        if wait_reason is not UNSET:
            field_dict["wait_reason"] = wait_reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_capacity_wait import EventRecoveryCapacityWait

        d = dict(src_dict)
        job_id = UUID(d.pop("job_id"))

        mode = check_event_recovery_job_health_mode(d.pop("mode"))

        state = check_event_recovery_job_health_state(d.pop("state"))

        status = check_event_recovery_job_health_status(d.pop("status"))

        pending_count = d.pop("pending_count")

        rate_per_second = d.pop("rate_per_second")

        progress_age_seconds = d.pop("progress_age_seconds")

        progress_known = d.pop("progress_known")

        next_attempt_at = datetime.datetime.fromisoformat(d.pop("next_attempt_at"))

        eligible_at = datetime.datetime.fromisoformat(d.pop("eligible_at"))

        overdue_seconds = d.pop("overdue_seconds")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        expiring = d.pop("expiring")

        _capacity_wait = d.pop("capacity_wait", UNSET)
        capacity_wait: EventRecoveryCapacityWait | Unset
        if isinstance(_capacity_wait, Unset):
            capacity_wait = UNSET
        else:
            capacity_wait = EventRecoveryCapacityWait.from_dict(_capacity_wait)

        _last_progress_at = d.pop("last_progress_at", UNSET)
        last_progress_at: datetime.datetime | Unset
        if isinstance(_last_progress_at, Unset):
            last_progress_at = UNSET
        else:
            last_progress_at = datetime.datetime.fromisoformat(_last_progress_at)

        _wait_reason = d.pop("wait_reason", UNSET)
        wait_reason: EventRecoveryJobHealthWaitReason | Unset
        if isinstance(_wait_reason, Unset):
            wait_reason = UNSET
        else:
            wait_reason = check_event_recovery_job_health_wait_reason(_wait_reason)

        event_recovery_job_health = cls(
            job_id=job_id,
            mode=mode,
            state=state,
            status=status,
            pending_count=pending_count,
            rate_per_second=rate_per_second,
            progress_age_seconds=progress_age_seconds,
            progress_known=progress_known,
            next_attempt_at=next_attempt_at,
            eligible_at=eligible_at,
            overdue_seconds=overdue_seconds,
            expires_at=expires_at,
            expiring=expiring,
            capacity_wait=capacity_wait,
            last_progress_at=last_progress_at,
            wait_reason=wait_reason,
        )

        return event_recovery_job_health
