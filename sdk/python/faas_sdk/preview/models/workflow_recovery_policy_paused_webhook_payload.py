from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="WorkflowRecoveryPolicyPausedWebhookPayload")


@_attrs_define
class WorkflowRecoveryPolicyPausedWebhookPayload:
    """Historical automation.recovery_policy_paused event emitted once per policy version after repeated proven automatic
    retry failures. Frozen receivers and stable event identity survive retries and later policy updates.

    """

    event_id: UUID
    app_id: UUID
    app: str
    policy_version: int
    paused_at: datetime.datetime
    consecutive_failures: int
    reason: str
    policy_url: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event_id = str(self.event_id)

        app_id = str(self.app_id)

        app = self.app

        policy_version = self.policy_version

        paused_at = self.paused_at.isoformat()

        consecutive_failures = self.consecutive_failures

        reason = self.reason

        policy_url = self.policy_url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event_id": event_id,
                "app_id": app_id,
                "app": app,
                "policy_version": policy_version,
                "paused_at": paused_at,
                "consecutive_failures": consecutive_failures,
                "reason": reason,
                "policy_url": policy_url,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_id = UUID(d.pop("event_id"))

        app_id = UUID(d.pop("app_id"))

        app = d.pop("app")

        policy_version = d.pop("policy_version")

        paused_at = datetime.datetime.fromisoformat(d.pop("paused_at"))

        consecutive_failures = d.pop("consecutive_failures")

        reason = d.pop("reason")

        policy_url = d.pop("policy_url")

        workflow_recovery_policy_paused_webhook_payload = cls(
            event_id=event_id,
            app_id=app_id,
            app=app,
            policy_version=policy_version,
            paused_at=paused_at,
            consecutive_failures=consecutive_failures,
            reason=reason,
            policy_url=policy_url,
        )

        workflow_recovery_policy_paused_webhook_payload.additional_properties = d
        return workflow_recovery_policy_paused_webhook_payload

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
