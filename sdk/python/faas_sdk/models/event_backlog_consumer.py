from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_backlog_consumer_consumer_kind import (
    EventBacklogConsumerConsumerKind,
    check_event_backlog_consumer_consumer_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_backlog_consumer_pending_waiting_reasons import EventBacklogConsumerPendingWaitingReasons


T = TypeVar("T", bound="EventBacklogConsumer")


@_attrs_define
class EventBacklogConsumer:
    """Exact counts over all matching waiting recipients for one app/subscription/consumer kind, independent of recipient
    pagination.

    """

    app_id: UUID
    app_slug: str
    target_available: bool
    subscription_id: str
    consumer_kind: EventBacklogConsumerConsumerKind
    waiting_recipients: int
    pending_recipients: int
    processing_recipients: int
    capacity_waiting_recipients: int
    ordering_waiting_recipients: int
    oldest_accepted_at: datetime.datetime
    oldest_age_seconds: float
    pending_waiting_reasons: EventBacklogConsumerPendingWaitingReasons | Unset = UNSET
    """Exact mutually exclusive effective waiting-reason counts over matching pending recipients, including zero
    values for known reasons. Independent of recipient pagination."""
    capacity_consumer_waiting_recipients: int | Unset = UNSET
    """Complete count with effective capacity_consumer waiting reason."""
    capacity_app_waiting_recipients: int | Unset = UNSET
    """Complete count with effective capacity_app waiting reason."""
    capacity_account_waiting_recipients: int | Unset = UNSET
    """Complete count with effective capacity_account waiting reason."""

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        app_slug = self.app_slug

        target_available = self.target_available

        subscription_id = self.subscription_id

        consumer_kind: str = self.consumer_kind

        waiting_recipients = self.waiting_recipients

        pending_recipients = self.pending_recipients

        processing_recipients = self.processing_recipients

        capacity_waiting_recipients = self.capacity_waiting_recipients

        ordering_waiting_recipients = self.ordering_waiting_recipients

        oldest_accepted_at = self.oldest_accepted_at.isoformat()

        oldest_age_seconds = self.oldest_age_seconds

        pending_waiting_reasons: dict[str, Any] | Unset = UNSET
        if not isinstance(self.pending_waiting_reasons, Unset):
            pending_waiting_reasons = self.pending_waiting_reasons.to_dict()

        capacity_consumer_waiting_recipients = self.capacity_consumer_waiting_recipients

        capacity_app_waiting_recipients = self.capacity_app_waiting_recipients

        capacity_account_waiting_recipients = self.capacity_account_waiting_recipients

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "app_slug": app_slug,
                "target_available": target_available,
                "subscription_id": subscription_id,
                "consumer_kind": consumer_kind,
                "waiting_recipients": waiting_recipients,
                "pending_recipients": pending_recipients,
                "processing_recipients": processing_recipients,
                "capacity_waiting_recipients": capacity_waiting_recipients,
                "ordering_waiting_recipients": ordering_waiting_recipients,
                "oldest_accepted_at": oldest_accepted_at,
                "oldest_age_seconds": oldest_age_seconds,
            }
        )
        if pending_waiting_reasons is not UNSET:
            field_dict["pending_waiting_reasons"] = pending_waiting_reasons
        if capacity_consumer_waiting_recipients is not UNSET:
            field_dict["capacity_consumer_waiting_recipients"] = capacity_consumer_waiting_recipients
        if capacity_app_waiting_recipients is not UNSET:
            field_dict["capacity_app_waiting_recipients"] = capacity_app_waiting_recipients
        if capacity_account_waiting_recipients is not UNSET:
            field_dict["capacity_account_waiting_recipients"] = capacity_account_waiting_recipients

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_backlog_consumer_pending_waiting_reasons import EventBacklogConsumerPendingWaitingReasons

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        app_slug = d.pop("app_slug")

        target_available = d.pop("target_available")

        subscription_id = d.pop("subscription_id")

        consumer_kind = check_event_backlog_consumer_consumer_kind(d.pop("consumer_kind"))

        waiting_recipients = d.pop("waiting_recipients")

        pending_recipients = d.pop("pending_recipients")

        processing_recipients = d.pop("processing_recipients")

        capacity_waiting_recipients = d.pop("capacity_waiting_recipients")

        ordering_waiting_recipients = d.pop("ordering_waiting_recipients")

        oldest_accepted_at = datetime.datetime.fromisoformat(d.pop("oldest_accepted_at"))

        oldest_age_seconds = d.pop("oldest_age_seconds")

        _pending_waiting_reasons = d.pop("pending_waiting_reasons", UNSET)
        pending_waiting_reasons: EventBacklogConsumerPendingWaitingReasons | Unset
        if isinstance(_pending_waiting_reasons, Unset):
            pending_waiting_reasons = UNSET
        else:
            pending_waiting_reasons = EventBacklogConsumerPendingWaitingReasons.from_dict(_pending_waiting_reasons)

        capacity_consumer_waiting_recipients = d.pop("capacity_consumer_waiting_recipients", UNSET)

        capacity_app_waiting_recipients = d.pop("capacity_app_waiting_recipients", UNSET)

        capacity_account_waiting_recipients = d.pop("capacity_account_waiting_recipients", UNSET)

        event_backlog_consumer = cls(
            app_id=app_id,
            app_slug=app_slug,
            target_available=target_available,
            subscription_id=subscription_id,
            consumer_kind=consumer_kind,
            waiting_recipients=waiting_recipients,
            pending_recipients=pending_recipients,
            processing_recipients=processing_recipients,
            capacity_waiting_recipients=capacity_waiting_recipients,
            ordering_waiting_recipients=ordering_waiting_recipients,
            oldest_accepted_at=oldest_accepted_at,
            oldest_age_seconds=oldest_age_seconds,
            pending_waiting_reasons=pending_waiting_reasons,
            capacity_consumer_waiting_recipients=capacity_consumer_waiting_recipients,
            capacity_app_waiting_recipients=capacity_app_waiting_recipients,
            capacity_account_waiting_recipients=capacity_account_waiting_recipients,
        )

        return event_backlog_consumer
