from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_recovery_notification_retry_target_kind import (
    EventRecoveryNotificationRetryTargetKind,
    check_event_recovery_notification_retry_target_kind,
)

T = TypeVar("T", bound="EventRecoveryNotificationRetryTarget")


@_attrs_define
class EventRecoveryNotificationRetryTarget:
    """Exact receiver selection and expected delivery generation copied from a recovery notification retry preview.
    Repeated delivery IDs are rejected.

    """

    kind: EventRecoveryNotificationRetryTargetKind
    webhook_id: UUID
    delivery_id: UUID
    expected_replay_generation: int

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        webhook_id = str(self.webhook_id)

        delivery_id = str(self.delivery_id)

        expected_replay_generation = self.expected_replay_generation

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
                "webhook_id": webhook_id,
                "delivery_id": delivery_id,
                "expected_replay_generation": expected_replay_generation,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_event_recovery_notification_retry_target_kind(d.pop("kind"))

        webhook_id = UUID(d.pop("webhook_id"))

        delivery_id = UUID(d.pop("delivery_id"))

        expected_replay_generation = d.pop("expected_replay_generation")

        event_recovery_notification_retry_target = cls(
            kind=kind,
            webhook_id=webhook_id,
            delivery_id=delivery_id,
            expected_replay_generation=expected_replay_generation,
        )

        return event_recovery_notification_retry_target
