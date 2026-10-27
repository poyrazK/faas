from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_recovery_notifications_receiver_limit import (
    EventRecoveryNotificationsReceiverLimit,
    check_event_recovery_notifications_receiver_limit,
)

if TYPE_CHECKING:
    from ..models.event_recovery_notification import EventRecoveryNotification


T = TypeVar("T", bound="EventRecoveryNotifications")


@_attrs_define
class EventRecoveryNotifications:
    """Metadata-only recovery notification report. A captured event does not imply acknowledgement. Reads do not capture
    events, relay the outbox or retry deliveries. Missing or pruned receiver evidence remains unknown.

    """

    job_id: UUID
    app_id: UUID
    app_slug: str
    observed_at: datetime.datetime
    receiver_limit: EventRecoveryNotificationsReceiverLimit
    notifications: list[EventRecoveryNotification]

    def to_dict(self) -> dict[str, Any]:
        job_id = str(self.job_id)

        app_id = str(self.app_id)

        app_slug = self.app_slug

        observed_at = self.observed_at.isoformat()

        receiver_limit: int = self.receiver_limit

        notifications = []
        for notifications_item_data in self.notifications:
            notifications_item = notifications_item_data.to_dict()
            notifications.append(notifications_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "job_id": job_id,
                "app_id": app_id,
                "app_slug": app_slug,
                "observed_at": observed_at,
                "receiver_limit": receiver_limit,
                "notifications": notifications,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_notification import EventRecoveryNotification

        d = dict(src_dict)
        job_id = UUID(d.pop("job_id"))

        app_id = UUID(d.pop("app_id"))

        app_slug = d.pop("app_slug")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        receiver_limit = check_event_recovery_notifications_receiver_limit(d.pop("receiver_limit"))

        notifications = []
        _notifications = d.pop("notifications")
        for notifications_item_data in _notifications:
            notifications_item = EventRecoveryNotification.from_dict(notifications_item_data)

            notifications.append(notifications_item)

        event_recovery_notifications = cls(
            job_id=job_id,
            app_id=app_id,
            app_slug=app_slug,
            observed_at=observed_at,
            receiver_limit=receiver_limit,
            notifications=notifications,
        )

        return event_recovery_notifications
