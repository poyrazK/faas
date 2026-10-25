from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="ManagedRealtimeNotificationRescheduleRequest")


@_attrs_define
class ManagedRealtimeNotificationRescheduleRequest:
    """Replacement not-before instant for pending notification delivery work."""

    notification_not_before: datetime.datetime
    """RFC3339, up to 48 hours ahead; must precede expiration."""

    def to_dict(self) -> dict[str, Any]:
        notification_not_before = self.notification_not_before.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "notification_not_before": notification_not_before,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        notification_not_before = datetime.datetime.fromisoformat(d.pop("notification_not_before"))

        managed_realtime_notification_reschedule_request = cls(
            notification_not_before=notification_not_before,
        )

        return managed_realtime_notification_reschedule_request
