from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_recovery_notification_receiver_status import (
    EventRecoveryNotificationReceiverStatus,
    check_event_recovery_notification_receiver_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventRecoveryNotificationReceiver")


@_attrs_define
class EventRecoveryNotificationReceiver:
    """A receiver from the frozen selection or retained delivery evidence. awaiting_relay requires a retained outbox;
    otherwise absent delivery evidence is unknown. Attempt counts reset on manual retry; replay_generation distinguishes
    budgets. No payloads, target URLs, errors, headers or secrets are included.

    """

    webhook_id: UUID
    receiver_available: bool
    """The selected subscription still exists in this account and app; this does not assert that it is enabled or
    its target is reachable."""
    status: EventRecoveryNotificationReceiverStatus
    attempt: int
    replay_generation: int
    last_response_code: int
    delivery_id: UUID | Unset = UNSET
    next_attempt_at: datetime.datetime | Unset = UNSET
    delivered_at: datetime.datetime | Unset = UNSET
    attempts_path: str | Unset = UNSET
    """Relative path to existing paginated attempt history, when the delivery and subscription are retained."""
    retry_path: str | Unset = UNSET
    """Relative POST path for existing independent dead-letter retry, when available. Reporting never invokes it."""

    def to_dict(self) -> dict[str, Any]:
        webhook_id = str(self.webhook_id)

        receiver_available = self.receiver_available

        status: str = self.status

        attempt = self.attempt

        replay_generation = self.replay_generation

        last_response_code = self.last_response_code

        delivery_id: str | Unset = UNSET
        if not isinstance(self.delivery_id, Unset):
            delivery_id = str(self.delivery_id)

        next_attempt_at: str | Unset = UNSET
        if not isinstance(self.next_attempt_at, Unset):
            next_attempt_at = self.next_attempt_at.isoformat()

        delivered_at: str | Unset = UNSET
        if not isinstance(self.delivered_at, Unset):
            delivered_at = self.delivered_at.isoformat()

        attempts_path = self.attempts_path

        retry_path = self.retry_path

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "webhook_id": webhook_id,
                "receiver_available": receiver_available,
                "status": status,
                "attempt": attempt,
                "replay_generation": replay_generation,
                "last_response_code": last_response_code,
            }
        )
        if delivery_id is not UNSET:
            field_dict["delivery_id"] = delivery_id
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at
        if delivered_at is not UNSET:
            field_dict["delivered_at"] = delivered_at
        if attempts_path is not UNSET:
            field_dict["attempts_path"] = attempts_path
        if retry_path is not UNSET:
            field_dict["retry_path"] = retry_path

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        webhook_id = UUID(d.pop("webhook_id"))

        receiver_available = d.pop("receiver_available")

        status = check_event_recovery_notification_receiver_status(d.pop("status"))

        attempt = d.pop("attempt")

        replay_generation = d.pop("replay_generation")

        last_response_code = d.pop("last_response_code")

        _delivery_id = d.pop("delivery_id", UNSET)
        delivery_id: UUID | Unset
        if isinstance(_delivery_id, Unset):
            delivery_id = UNSET
        else:
            delivery_id = UUID(_delivery_id)

        _next_attempt_at = d.pop("next_attempt_at", UNSET)
        next_attempt_at: datetime.datetime | Unset
        if isinstance(_next_attempt_at, Unset):
            next_attempt_at = UNSET
        else:
            next_attempt_at = datetime.datetime.fromisoformat(_next_attempt_at)

        _delivered_at = d.pop("delivered_at", UNSET)
        delivered_at: datetime.datetime | Unset
        if isinstance(_delivered_at, Unset):
            delivered_at = UNSET
        else:
            delivered_at = datetime.datetime.fromisoformat(_delivered_at)

        attempts_path = d.pop("attempts_path", UNSET)

        retry_path = d.pop("retry_path", UNSET)

        event_recovery_notification_receiver = cls(
            webhook_id=webhook_id,
            receiver_available=receiver_available,
            status=status,
            attempt=attempt,
            replay_generation=replay_generation,
            last_response_code=last_response_code,
            delivery_id=delivery_id,
            next_attempt_at=next_attempt_at,
            delivered_at=delivered_at,
            attempts_path=attempts_path,
            retry_path=retry_path,
        )

        return event_recovery_notification_receiver
