from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_health_notification_status import (
    WorkflowRecoveryHealthNotificationStatus,
    check_workflow_recovery_health_notification_status,
)

T = TypeVar("T", bound="WorkflowRecoveryHealthNotification")


@_attrs_define
class WorkflowRecoveryHealthNotification:
    """Frozen health notification identity and receiver set; no_receivers is terminal for this transition and does not
    replay to late receivers.

    """

    event_id: UUID
    status: WorkflowRecoveryHealthNotificationStatus
    captured_at: datetime.datetime
    receiver_webhook_ids: list[UUID]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event_id = str(self.event_id)

        status: str = self.status

        captured_at = self.captured_at.isoformat()

        receiver_webhook_ids = []
        for receiver_webhook_ids_item_data in self.receiver_webhook_ids:
            receiver_webhook_ids_item = str(receiver_webhook_ids_item_data)
            receiver_webhook_ids.append(receiver_webhook_ids_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event_id": event_id,
                "status": status,
                "captured_at": captured_at,
                "receiver_webhook_ids": receiver_webhook_ids,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_id = UUID(d.pop("event_id"))

        status = check_workflow_recovery_health_notification_status(d.pop("status"))

        captured_at = datetime.datetime.fromisoformat(d.pop("captured_at"))

        receiver_webhook_ids = []
        _receiver_webhook_ids = d.pop("receiver_webhook_ids")
        for receiver_webhook_ids_item_data in _receiver_webhook_ids:
            receiver_webhook_ids_item = UUID(receiver_webhook_ids_item_data)

            receiver_webhook_ids.append(receiver_webhook_ids_item)

        workflow_recovery_health_notification = cls(
            event_id=event_id,
            status=status,
            captured_at=captured_at,
            receiver_webhook_ids=receiver_webhook_ids,
        )

        workflow_recovery_health_notification.additional_properties = d
        return workflow_recovery_health_notification

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
