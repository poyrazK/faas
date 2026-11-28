from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.event_recovery_notification_retry_target import EventRecoveryNotificationRetryTarget


T = TypeVar("T", bound="EventRecoveryNotificationRetryRequest")


@_attrs_define
class EventRecoveryNotificationRetryRequest:
    """Explicit retry intent with a canonical nonzero UUID request identity. Target order is ignored for idempotency;
    target fields are immutable under this identity.

    """

    request_id: UUID
    targets: list[EventRecoveryNotificationRetryTarget]

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        targets = []
        for targets_item_data in self.targets:
            targets_item = targets_item_data.to_dict()
            targets.append(targets_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_id": request_id,
                "targets": targets,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_notification_retry_target import EventRecoveryNotificationRetryTarget

        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        targets = []
        _targets = d.pop("targets")
        for targets_item_data in _targets:
            targets_item = EventRecoveryNotificationRetryTarget.from_dict(targets_item_data)

            targets.append(targets_item)

        event_recovery_notification_retry_request = cls(
            request_id=request_id,
            targets=targets,
        )

        return event_recovery_notification_retry_request
