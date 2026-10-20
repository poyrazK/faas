from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.event_recovery_notification_retry_decision_summary_status import (
    EventRecoveryNotificationRetryDecisionSummaryStatus,
    check_event_recovery_notification_retry_decision_summary_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventRecoveryNotificationRetryDecisionSummary")


@_attrs_define
class EventRecoveryNotificationRetryDecisionSummary:
    """Original retry request counts and decision time with current outcomes for its queued generations, observed at the
    history response timestamp.

    """

    request_id: UUID
    decided_at: datetime.datetime
    target_count: int
    queued_count: int
    skipped_count: int
    succeeded_count: int
    failed_count: int
    pending_count: int
    unknown_count: int
    status: EventRecoveryNotificationRetryDecisionSummaryStatus
    """Aggregate outcome for original queued generations. Known failure takes precedence, then unknown evidence or
    no queued targets, then pending; success requires every queued target succeeded."""
    evidence_complete: bool
    """True when every queued target has a known outcome and complete retained attempt sequence. All-skipped
    requests have complete empty evidence but are inconclusive."""
    completed_at: datetime.datetime | Unset = UNSET
    """Latest terminal attempt finish time, present only when every queued target has a retained terminal outcome
    and at least one target was queued."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        decided_at = self.decided_at.isoformat()

        target_count = self.target_count

        queued_count = self.queued_count

        skipped_count = self.skipped_count

        succeeded_count = self.succeeded_count

        failed_count = self.failed_count

        pending_count = self.pending_count

        unknown_count = self.unknown_count

        status: str = self.status

        evidence_complete = self.evidence_complete

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request_id": request_id,
                "decided_at": decided_at,
                "target_count": target_count,
                "queued_count": queued_count,
                "skipped_count": skipped_count,
                "succeeded_count": succeeded_count,
                "failed_count": failed_count,
                "pending_count": pending_count,
                "unknown_count": unknown_count,
                "status": status,
                "evidence_complete": evidence_complete,
            }
        )
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        decided_at = datetime.datetime.fromisoformat(d.pop("decided_at"))

        target_count = d.pop("target_count")

        queued_count = d.pop("queued_count")

        skipped_count = d.pop("skipped_count")

        succeeded_count = d.pop("succeeded_count")

        failed_count = d.pop("failed_count")

        pending_count = d.pop("pending_count")

        unknown_count = d.pop("unknown_count")

        status = check_event_recovery_notification_retry_decision_summary_status(d.pop("status"))

        evidence_complete = d.pop("evidence_complete")

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        event_recovery_notification_retry_decision_summary = cls(
            request_id=request_id,
            decided_at=decided_at,
            target_count=target_count,
            queued_count=queued_count,
            skipped_count=skipped_count,
            succeeded_count=succeeded_count,
            failed_count=failed_count,
            pending_count=pending_count,
            unknown_count=unknown_count,
            status=status,
            evidence_complete=evidence_complete,
            completed_at=completed_at,
        )

        event_recovery_notification_retry_decision_summary.additional_properties = d
        return event_recovery_notification_retry_decision_summary

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
