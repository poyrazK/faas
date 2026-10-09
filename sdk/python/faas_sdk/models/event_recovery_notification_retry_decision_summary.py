from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="EventRecoveryNotificationRetryDecisionSummary")


@_attrs_define
class EventRecoveryNotificationRetryDecisionSummary:
    """Compact immutable retry request count and decision time; use the detail endpoint for original per receiver outcomes
    and current delivery state.

    """

    request_id: UUID
    decided_at: datetime.datetime
    target_count: int
    queued_count: int
    skipped_count: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        decided_at = self.decided_at.isoformat()

        target_count = self.target_count

        queued_count = self.queued_count

        skipped_count = self.skipped_count

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request_id": request_id,
                "decided_at": decided_at,
                "target_count": target_count,
                "queued_count": queued_count,
                "skipped_count": skipped_count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        decided_at = datetime.datetime.fromisoformat(d.pop("decided_at"))

        target_count = d.pop("target_count")

        queued_count = d.pop("queued_count")

        skipped_count = d.pop("skipped_count")

        event_recovery_notification_retry_decision_summary = cls(
            request_id=request_id,
            decided_at=decided_at,
            target_count=target_count,
            queued_count=queued_count,
            skipped_count=skipped_count,
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
