from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_queue_blocked_webhook_payload_reason import (
    WorkflowRecoveryQueueBlockedWebhookPayloadReason,
    check_workflow_recovery_queue_blocked_webhook_payload_reason,
)
from ..models.workflow_recovery_queue_blocked_webhook_payload_status import (
    WorkflowRecoveryQueueBlockedWebhookPayloadStatus,
    check_workflow_recovery_queue_blocked_webhook_payload_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowRecoveryQueueBlockedWebhookPayload")


@_attrs_define
class WorkflowRecoveryQueueBlockedWebhookPayload:
    """Historical automation.recovery_queue_blocked event captured once per automatic retry action when fresh inspection
    cannot resolve pending or applied evidence. Capturing this event does not finalize a child, release its execution
    slot, or authorize another retry. Delivery may occur after the block resolves; stable identity and frozen recipients
    support receiver deduplication.

    """

    event_id: UUID
    app_id: UUID
    app: str
    review_id: UUID
    action_id: UUID
    policy_version: int
    position: int
    status: WorkflowRecoveryQueueBlockedWebhookPayloadStatus
    reason: WorkflowRecoveryQueueBlockedWebhookPayloadReason
    observed_at: datetime.datetime
    queue_url: str
    review_status_url: str
    child_job_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event_id = str(self.event_id)

        app_id = str(self.app_id)

        app = self.app

        review_id = str(self.review_id)

        action_id = str(self.action_id)

        policy_version = self.policy_version

        position = self.position

        status: str = self.status

        reason: str = self.reason

        observed_at = self.observed_at.isoformat()

        queue_url = self.queue_url

        review_status_url = self.review_status_url

        child_job_id: str | Unset = UNSET
        if not isinstance(self.child_job_id, Unset):
            child_job_id = str(self.child_job_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event_id": event_id,
                "app_id": app_id,
                "app": app,
                "review_id": review_id,
                "action_id": action_id,
                "policy_version": policy_version,
                "position": position,
                "status": status,
                "reason": reason,
                "observed_at": observed_at,
                "queue_url": queue_url,
                "review_status_url": review_status_url,
            }
        )
        if child_job_id is not UNSET:
            field_dict["child_job_id"] = child_job_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_id = UUID(d.pop("event_id"))

        app_id = UUID(d.pop("app_id"))

        app = d.pop("app")

        review_id = UUID(d.pop("review_id"))

        action_id = UUID(d.pop("action_id"))

        policy_version = d.pop("policy_version")

        position = d.pop("position")

        status = check_workflow_recovery_queue_blocked_webhook_payload_status(d.pop("status"))

        reason = check_workflow_recovery_queue_blocked_webhook_payload_reason(d.pop("reason"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        queue_url = d.pop("queue_url")

        review_status_url = d.pop("review_status_url")

        _child_job_id = d.pop("child_job_id", UNSET)
        child_job_id: UUID | Unset
        if isinstance(_child_job_id, Unset):
            child_job_id = UNSET
        else:
            child_job_id = UUID(_child_job_id)

        workflow_recovery_queue_blocked_webhook_payload = cls(
            event_id=event_id,
            app_id=app_id,
            app=app,
            review_id=review_id,
            action_id=action_id,
            policy_version=policy_version,
            position=position,
            status=status,
            reason=reason,
            observed_at=observed_at,
            queue_url=queue_url,
            review_status_url=review_status_url,
            child_job_id=child_job_id,
        )

        workflow_recovery_queue_blocked_webhook_payload.additional_properties = d
        return workflow_recovery_queue_blocked_webhook_payload

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
