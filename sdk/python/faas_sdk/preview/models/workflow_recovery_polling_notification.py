from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.workflow_recovery_polling_notification_status import (
    WorkflowRecoveryPollingNotificationStatus,
    check_workflow_recovery_polling_notification_status,
)

if TYPE_CHECKING:
    from ..models.workflow_recovery_polling_webhook_payload import WorkflowRecoveryPollingWebhookPayload


T = TypeVar("T", bound="WorkflowRecoveryPollingNotification")


@_attrs_define
class WorkflowRecoveryPollingNotification:
    """Last captured polling alert receipt. queued means durable outbox admission; no_receivers never replays to later
    subscribers. Successful polling retains the recovered receipt separately from the cleared error metadata.

    """

    payload: WorkflowRecoveryPollingWebhookPayload
    """Historical automation.recovery_polling_status event. Three consecutive deterministic polling failures
    trigger failed; unchanged code and policy version stay quiet. Recovered proves a successful poll, independently
    of workflow outcome. Recipients and payload are frozen at capture; use event_id for deduplication."""
    status: WorkflowRecoveryPollingNotificationStatus
    captured_at: datetime.datetime
    receiver_webhook_ids: list[UUID]

    def to_dict(self) -> dict[str, Any]:
        payload = self.payload.to_dict()

        status: str = self.status

        captured_at = self.captured_at.isoformat()

        receiver_webhook_ids = []
        for receiver_webhook_ids_item_data in self.receiver_webhook_ids:
            receiver_webhook_ids_item = str(receiver_webhook_ids_item_data)
            receiver_webhook_ids.append(receiver_webhook_ids_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "payload": payload,
                "status": status,
                "captured_at": captured_at,
                "receiver_webhook_ids": receiver_webhook_ids,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_polling_webhook_payload import WorkflowRecoveryPollingWebhookPayload

        d = dict(src_dict)
        payload = WorkflowRecoveryPollingWebhookPayload.from_dict(d.pop("payload"))

        status = check_workflow_recovery_polling_notification_status(d.pop("status"))

        captured_at = datetime.datetime.fromisoformat(d.pop("captured_at"))

        receiver_webhook_ids = []
        _receiver_webhook_ids = d.pop("receiver_webhook_ids")
        for receiver_webhook_ids_item_data in _receiver_webhook_ids:
            receiver_webhook_ids_item = UUID(receiver_webhook_ids_item_data)

            receiver_webhook_ids.append(receiver_webhook_ids_item)

        workflow_recovery_polling_notification = cls(
            payload=payload,
            status=status,
            captured_at=captured_at,
            receiver_webhook_ids=receiver_webhook_ids,
        )

        return workflow_recovery_polling_notification
