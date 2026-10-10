from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.event_recovery_notification_retry_decision_summary import (
        EventRecoveryNotificationRetryDecisionSummary,
    )


T = TypeVar("T", bound="EventRecoveryNotificationRetryBacklogRequest")


@_attrs_define
class EventRecoveryNotificationRetryBacklogRequest:
    """One saved retry request found in an owned retained app recovery job, with original-generation outcome summary and
    metadata-only inspection paths.

    """

    job_id: UUID
    job_created_at: datetime.datetime
    summary: EventRecoveryNotificationRetryDecisionSummary
    """Original retry request counts and decision time with current outcomes for its queued generations, observed
    at the history response timestamp."""
    detail_path: str
    retry_preview_path: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        job_id = str(self.job_id)

        job_created_at = self.job_created_at.isoformat()

        summary = self.summary.to_dict()

        detail_path = self.detail_path

        retry_preview_path = self.retry_preview_path

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "job_id": job_id,
                "job_created_at": job_created_at,
                "summary": summary,
                "detail_path": detail_path,
                "retry_preview_path": retry_preview_path,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_notification_retry_decision_summary import (
            EventRecoveryNotificationRetryDecisionSummary,
        )

        d = dict(src_dict)
        job_id = UUID(d.pop("job_id"))

        job_created_at = datetime.datetime.fromisoformat(d.pop("job_created_at"))

        summary = EventRecoveryNotificationRetryDecisionSummary.from_dict(d.pop("summary"))

        detail_path = d.pop("detail_path")

        retry_preview_path = d.pop("retry_preview_path")

        event_recovery_notification_retry_backlog_request = cls(
            job_id=job_id,
            job_created_at=job_created_at,
            summary=summary,
            detail_path=detail_path,
            retry_preview_path=retry_preview_path,
        )

        event_recovery_notification_retry_backlog_request.additional_properties = d
        return event_recovery_notification_retry_backlog_request

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
