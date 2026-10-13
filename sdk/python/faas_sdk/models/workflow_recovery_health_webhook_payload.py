from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_health_webhook_payload_status import (
    WorkflowRecoveryHealthWebhookPayloadStatus,
    check_workflow_recovery_health_webhook_payload_status,
)

T = TypeVar("T", bound="WorkflowRecoveryHealthWebhookPayload")


@_attrs_define
class WorkflowRecoveryHealthWebhookPayload:
    """Historical automation.recovery_health_alert or automation.recovery_health_recovered transition with stable event and
    incident identities. Recovery targets only original alert receivers still enabled and subscribed to recovery at
    capture. Delivery can occur after the observed condition changes and does not authorize retries or release slots.

    """

    event_id: UUID
    episode_id: UUID
    app_id: UUID
    app: str
    policy_version: int
    status: WorkflowRecoveryHealthWebhookPayloadStatus
    reason: str
    opened_at: datetime.datetime
    observed_at: datetime.datetime
    pending: int
    outstanding: int
    queue_truncated: bool
    health_url: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event_id = str(self.event_id)

        episode_id = str(self.episode_id)

        app_id = str(self.app_id)

        app = self.app

        policy_version = self.policy_version

        status: str = self.status

        reason = self.reason

        opened_at = self.opened_at.isoformat()

        observed_at = self.observed_at.isoformat()

        pending = self.pending

        outstanding = self.outstanding

        queue_truncated = self.queue_truncated

        health_url = self.health_url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event_id": event_id,
                "episode_id": episode_id,
                "app_id": app_id,
                "app": app,
                "policy_version": policy_version,
                "status": status,
                "reason": reason,
                "opened_at": opened_at,
                "observed_at": observed_at,
                "pending": pending,
                "outstanding": outstanding,
                "queue_truncated": queue_truncated,
                "health_url": health_url,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_id = UUID(d.pop("event_id"))

        episode_id = UUID(d.pop("episode_id"))

        app_id = UUID(d.pop("app_id"))

        app = d.pop("app")

        policy_version = d.pop("policy_version")

        status = check_workflow_recovery_health_webhook_payload_status(d.pop("status"))

        reason = d.pop("reason")

        opened_at = datetime.datetime.fromisoformat(d.pop("opened_at"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        pending = d.pop("pending")

        outstanding = d.pop("outstanding")

        queue_truncated = d.pop("queue_truncated")

        health_url = d.pop("health_url")

        workflow_recovery_health_webhook_payload = cls(
            event_id=event_id,
            episode_id=episode_id,
            app_id=app_id,
            app=app,
            policy_version=policy_version,
            status=status,
            reason=reason,
            opened_at=opened_at,
            observed_at=observed_at,
            pending=pending,
            outstanding=outstanding,
            queue_truncated=queue_truncated,
            health_url=health_url,
        )

        workflow_recovery_health_webhook_payload.additional_properties = d
        return workflow_recovery_health_webhook_payload

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
