from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.workflow_recovery_review_status import WorkflowRecoveryReviewStatus


T = TypeVar("T", bound="WorkflowRecoveryReviewFinishedWebhookPayload")


@_attrs_define
class WorkflowRecoveryReviewFinishedWebhookPayload:
    """Payload of automation.recovery_review_finished. All actions have proven final outcomes for their frozen generations.
    This historical result remains valid after review decisions or later retries; receivers deduplicate by event_id.

    """

    event_id: UUID
    review_id: UUID
    app_id: UUID
    app: str
    job_id: UUID
    policy_version: int
    captured_at: datetime.datetime
    result: WorkflowRecoveryReviewStatus
    """Application status and aggregate execution outcome. Complete requires final proof for every action; retained
    completion snapshots take precedence over later generations and pruned execution rows."""
    review_url: str
    status_url: str
    status_command: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event_id = str(self.event_id)

        review_id = str(self.review_id)

        app_id = str(self.app_id)

        app = self.app

        job_id = str(self.job_id)

        policy_version = self.policy_version

        captured_at = self.captured_at.isoformat()

        result = self.result.to_dict()

        review_url = self.review_url

        status_url = self.status_url

        status_command = self.status_command

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event_id": event_id,
                "review_id": review_id,
                "app_id": app_id,
                "app": app,
                "job_id": job_id,
                "policy_version": policy_version,
                "captured_at": captured_at,
                "result": result,
                "review_url": review_url,
                "status_url": status_url,
                "status_command": status_command,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_review_status import WorkflowRecoveryReviewStatus

        d = dict(src_dict)
        event_id = UUID(d.pop("event_id"))

        review_id = UUID(d.pop("review_id"))

        app_id = UUID(d.pop("app_id"))

        app = d.pop("app")

        job_id = UUID(d.pop("job_id"))

        policy_version = d.pop("policy_version")

        captured_at = datetime.datetime.fromisoformat(d.pop("captured_at"))

        result = WorkflowRecoveryReviewStatus.from_dict(d.pop("result"))

        review_url = d.pop("review_url")

        status_url = d.pop("status_url")

        status_command = d.pop("status_command")

        workflow_recovery_review_finished_webhook_payload = cls(
            event_id=event_id,
            review_id=review_id,
            app_id=app_id,
            app=app,
            job_id=job_id,
            policy_version=policy_version,
            captured_at=captured_at,
            result=result,
            review_url=review_url,
            status_url=status_url,
            status_command=status_command,
        )

        workflow_recovery_review_finished_webhook_payload.additional_properties = d
        return workflow_recovery_review_finished_webhook_payload

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
