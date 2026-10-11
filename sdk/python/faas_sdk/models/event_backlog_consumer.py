from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="EventBacklogConsumer")


@_attrs_define
class EventBacklogConsumer:
    """Exact counts over all matching waiting recipients for one captured app/subscription, independent of recipient
    pagination.

    """

    app_id: UUID
    app_slug: str
    target_available: bool
    subscription_id: str
    waiting_recipients: int
    pending_recipients: int
    processing_recipients: int
    capacity_waiting_recipients: int
    oldest_accepted_at: datetime.datetime
    oldest_age_seconds: float

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        app_slug = self.app_slug

        target_available = self.target_available

        subscription_id = self.subscription_id

        waiting_recipients = self.waiting_recipients

        pending_recipients = self.pending_recipients

        processing_recipients = self.processing_recipients

        capacity_waiting_recipients = self.capacity_waiting_recipients

        oldest_accepted_at = self.oldest_accepted_at.isoformat()

        oldest_age_seconds = self.oldest_age_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "app_slug": app_slug,
                "target_available": target_available,
                "subscription_id": subscription_id,
                "waiting_recipients": waiting_recipients,
                "pending_recipients": pending_recipients,
                "processing_recipients": processing_recipients,
                "capacity_waiting_recipients": capacity_waiting_recipients,
                "oldest_accepted_at": oldest_accepted_at,
                "oldest_age_seconds": oldest_age_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        app_slug = d.pop("app_slug")

        target_available = d.pop("target_available")

        subscription_id = d.pop("subscription_id")

        waiting_recipients = d.pop("waiting_recipients")

        pending_recipients = d.pop("pending_recipients")

        processing_recipients = d.pop("processing_recipients")

        capacity_waiting_recipients = d.pop("capacity_waiting_recipients")

        oldest_accepted_at = datetime.datetime.fromisoformat(d.pop("oldest_accepted_at"))

        oldest_age_seconds = d.pop("oldest_age_seconds")

        event_backlog_consumer = cls(
            app_id=app_id,
            app_slug=app_slug,
            target_available=target_available,
            subscription_id=subscription_id,
            waiting_recipients=waiting_recipients,
            pending_recipients=pending_recipients,
            processing_recipients=processing_recipients,
            capacity_waiting_recipients=capacity_waiting_recipients,
            oldest_accepted_at=oldest_accepted_at,
            oldest_age_seconds=oldest_age_seconds,
        )

        return event_backlog_consumer
