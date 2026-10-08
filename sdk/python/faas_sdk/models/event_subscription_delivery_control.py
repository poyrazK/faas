from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_circuit_breaker_response import EventCircuitBreakerResponse


T = TypeVar("T", bound="EventSubscriptionDeliveryControl")


@_attrs_define
class EventSubscriptionDeliveryControl:
    """Live operator controls for a captured application subscription. Pending and processing counts include publication
    and historical backfill recipients retained in the backlog.

    """

    subscription_id: UUID
    app_id: UUID
    paused: bool
    rate_per_second: int
    pending_recipients: int
    processing_recipients: int
    oldest_age_seconds: float
    circuit_breaker: EventCircuitBreakerResponse | Unset = UNSET
    oldest_pending_at: datetime.datetime | Unset = UNSET
    updated_at: datetime.datetime | Unset = UNSET
    """Absent until an operator sets a control."""

    def to_dict(self) -> dict[str, Any]:
        subscription_id = str(self.subscription_id)

        app_id = str(self.app_id)

        paused = self.paused

        rate_per_second = self.rate_per_second

        pending_recipients = self.pending_recipients

        processing_recipients = self.processing_recipients

        oldest_age_seconds = self.oldest_age_seconds

        circuit_breaker: dict[str, Any] | Unset = UNSET
        if not isinstance(self.circuit_breaker, Unset):
            circuit_breaker = self.circuit_breaker.to_dict()

        oldest_pending_at: str | Unset = UNSET
        if not isinstance(self.oldest_pending_at, Unset):
            oldest_pending_at = self.oldest_pending_at.isoformat()

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subscription_id": subscription_id,
                "app_id": app_id,
                "paused": paused,
                "rate_per_second": rate_per_second,
                "pending_recipients": pending_recipients,
                "processing_recipients": processing_recipients,
                "oldest_age_seconds": oldest_age_seconds,
            }
        )
        if circuit_breaker is not UNSET:
            field_dict["circuit_breaker"] = circuit_breaker
        if oldest_pending_at is not UNSET:
            field_dict["oldest_pending_at"] = oldest_pending_at
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_circuit_breaker_response import EventCircuitBreakerResponse

        d = dict(src_dict)
        subscription_id = UUID(d.pop("subscription_id"))

        app_id = UUID(d.pop("app_id"))

        paused = d.pop("paused")

        rate_per_second = d.pop("rate_per_second")

        pending_recipients = d.pop("pending_recipients")

        processing_recipients = d.pop("processing_recipients")

        oldest_age_seconds = d.pop("oldest_age_seconds")

        _circuit_breaker = d.pop("circuit_breaker", UNSET)
        circuit_breaker: EventCircuitBreakerResponse | Unset
        if isinstance(_circuit_breaker, Unset):
            circuit_breaker = UNSET
        else:
            circuit_breaker = EventCircuitBreakerResponse.from_dict(_circuit_breaker)

        _oldest_pending_at = d.pop("oldest_pending_at", UNSET)
        oldest_pending_at: datetime.datetime | Unset
        if isinstance(_oldest_pending_at, Unset):
            oldest_pending_at = UNSET
        else:
            oldest_pending_at = datetime.datetime.fromisoformat(_oldest_pending_at)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        event_subscription_delivery_control = cls(
            subscription_id=subscription_id,
            app_id=app_id,
            paused=paused,
            rate_per_second=rate_per_second,
            pending_recipients=pending_recipients,
            processing_recipients=processing_recipients,
            oldest_age_seconds=oldest_age_seconds,
            circuit_breaker=circuit_breaker,
            oldest_pending_at=oldest_pending_at,
            updated_at=updated_at,
        )

        return event_subscription_delivery_control
