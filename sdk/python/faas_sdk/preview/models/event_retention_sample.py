from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_retention_sample_hold_reason import (
    EventRetentionSampleHoldReason,
    check_event_retention_sample_hold_reason,
)
from ..models.event_retention_sample_status import EventRetentionSampleStatus, check_event_retention_sample_status

T = TypeVar("T", bound="EventRetentionSample")


@_attrs_define
class EventRetentionSample:
    """Receipt nearing or past its nominal retention deadline, without payloads."""

    event_source: str
    event_id: str
    accepted_at: datetime.datetime
    retain_until: datetime.datetime
    retained_bytes: int
    status: EventRetentionSampleStatus
    hold_reason: EventRetentionSampleHoldReason

    def to_dict(self) -> dict[str, Any]:
        event_source = self.event_source

        event_id = self.event_id

        accepted_at = self.accepted_at.isoformat()

        retain_until = self.retain_until.isoformat()

        retained_bytes = self.retained_bytes

        status: str = self.status

        hold_reason: str = self.hold_reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "event_source": event_source,
                "event_id": event_id,
                "accepted_at": accepted_at,
                "retain_until": retain_until,
                "retained_bytes": retained_bytes,
                "status": status,
                "hold_reason": hold_reason,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_source = d.pop("event_source")

        event_id = d.pop("event_id")

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        retain_until = datetime.datetime.fromisoformat(d.pop("retain_until"))

        retained_bytes = d.pop("retained_bytes")

        status = check_event_retention_sample_status(d.pop("status"))

        hold_reason = check_event_retention_sample_hold_reason(d.pop("hold_reason"))

        event_retention_sample = cls(
            event_source=event_source,
            event_id=event_id,
            accepted_at=accepted_at,
            retain_until=retain_until,
            retained_bytes=retained_bytes,
            status=status,
            hold_reason=hold_reason,
        )

        return event_retention_sample
