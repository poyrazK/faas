from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="AppWebhookDeliveryHealthResponse")


@_attrs_define
class AppWebhookDeliveryHealthResponse:
    """Current queue counts and terminal outcomes over the 24 hours ending at snapshot_at."""

    webhook_id: UUID
    snapshot_at: datetime.datetime
    pending_count: int
    in_flight_count: int
    dead_count: int
    recent_succeeded_count: int
    recent_dead_count: int
    receiver_cooldown_until: datetime.datetime | Unset = UNSET
    """Active receiver Retry-After deadline; new claims for this subscription resume when it expires."""
    oldest_overdue_at: datetime.datetime | Unset = UNSET
    """Earliest due time among claimable pending or expired in-flight deliveries; omitted during an active receiver cooldown."""
    oldest_overdue_seconds: int | Unset = UNSET
    """Age of the oldest overdue delivery at snapshot_at."""
    recent_success_rate: float | Unset = UNSET
    """Recent succeeded divided by recent succeeded plus dead; omitted when there are no terminal deliveries."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        webhook_id = str(self.webhook_id)

        snapshot_at = self.snapshot_at.isoformat()

        pending_count = self.pending_count

        in_flight_count = self.in_flight_count

        dead_count = self.dead_count

        recent_succeeded_count = self.recent_succeeded_count

        recent_dead_count = self.recent_dead_count

        receiver_cooldown_until: str | Unset = UNSET
        if not isinstance(self.receiver_cooldown_until, Unset):
            receiver_cooldown_until = self.receiver_cooldown_until.isoformat()

        oldest_overdue_at: str | Unset = UNSET
        if not isinstance(self.oldest_overdue_at, Unset):
            oldest_overdue_at = self.oldest_overdue_at.isoformat()

        oldest_overdue_seconds = self.oldest_overdue_seconds

        recent_success_rate = self.recent_success_rate

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "webhook_id": webhook_id,
                "snapshot_at": snapshot_at,
                "pending_count": pending_count,
                "in_flight_count": in_flight_count,
                "dead_count": dead_count,
                "recent_succeeded_count": recent_succeeded_count,
                "recent_dead_count": recent_dead_count,
            }
        )
        if receiver_cooldown_until is not UNSET:
            field_dict["receiver_cooldown_until"] = receiver_cooldown_until
        if oldest_overdue_at is not UNSET:
            field_dict["oldest_overdue_at"] = oldest_overdue_at
        if oldest_overdue_seconds is not UNSET:
            field_dict["oldest_overdue_seconds"] = oldest_overdue_seconds
        if recent_success_rate is not UNSET:
            field_dict["recent_success_rate"] = recent_success_rate

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        webhook_id = UUID(d.pop("webhook_id"))

        snapshot_at = datetime.datetime.fromisoformat(d.pop("snapshot_at"))

        pending_count = d.pop("pending_count")

        in_flight_count = d.pop("in_flight_count")

        dead_count = d.pop("dead_count")

        recent_succeeded_count = d.pop("recent_succeeded_count")

        recent_dead_count = d.pop("recent_dead_count")

        _receiver_cooldown_until = d.pop("receiver_cooldown_until", UNSET)
        receiver_cooldown_until: datetime.datetime | Unset
        if isinstance(_receiver_cooldown_until, Unset):
            receiver_cooldown_until = UNSET
        else:
            receiver_cooldown_until = datetime.datetime.fromisoformat(_receiver_cooldown_until)

        _oldest_overdue_at = d.pop("oldest_overdue_at", UNSET)
        oldest_overdue_at: datetime.datetime | Unset
        if isinstance(_oldest_overdue_at, Unset):
            oldest_overdue_at = UNSET
        else:
            oldest_overdue_at = datetime.datetime.fromisoformat(_oldest_overdue_at)

        oldest_overdue_seconds = d.pop("oldest_overdue_seconds", UNSET)

        recent_success_rate = d.pop("recent_success_rate", UNSET)

        app_webhook_delivery_health_response = cls(
            webhook_id=webhook_id,
            snapshot_at=snapshot_at,
            pending_count=pending_count,
            in_flight_count=in_flight_count,
            dead_count=dead_count,
            recent_succeeded_count=recent_succeeded_count,
            recent_dead_count=recent_dead_count,
            receiver_cooldown_until=receiver_cooldown_until,
            oldest_overdue_at=oldest_overdue_at,
            oldest_overdue_seconds=oldest_overdue_seconds,
            recent_success_rate=recent_success_rate,
        )

        app_webhook_delivery_health_response.additional_properties = d
        return app_webhook_delivery_health_response

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
