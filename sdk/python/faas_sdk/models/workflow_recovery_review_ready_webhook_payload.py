from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.workflow_recovery_policy_decision import WorkflowRecoveryPolicyDecision


T = TypeVar("T", bound="WorkflowRecoveryReviewReadyWebhookPayload")


@_attrs_define
class WorkflowRecoveryReviewReadyWebhookPayload:
    """Payload of automation.recovery_review_ready. Carries frozen prepared targets and review commands; approval and
    application remain explicit. Retries reuse the event identifier and delivery ledger.

    """

    event_id: UUID
    review_id: UUID
    app_id: UUID
    app: str
    job_id: UUID
    policy_version: int
    prepared_at: datetime.datetime
    observed_at: datetime.datetime
    targets: list[WorkflowRecoveryPolicyDecision]
    review_url: str
    status_url: str
    review_command: str
    export_command: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event_id = str(self.event_id)

        review_id = str(self.review_id)

        app_id = str(self.app_id)

        app = self.app

        job_id = str(self.job_id)

        policy_version = self.policy_version

        prepared_at = self.prepared_at.isoformat()

        observed_at = self.observed_at.isoformat()

        targets = []
        for targets_item_data in self.targets:
            targets_item = targets_item_data.to_dict()
            targets.append(targets_item)

        review_url = self.review_url

        status_url = self.status_url

        review_command = self.review_command

        export_command = self.export_command

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
                "prepared_at": prepared_at,
                "observed_at": observed_at,
                "targets": targets,
                "review_url": review_url,
                "status_url": status_url,
                "review_command": review_command,
                "export_command": export_command,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_policy_decision import WorkflowRecoveryPolicyDecision

        d = dict(src_dict)
        event_id = UUID(d.pop("event_id"))

        review_id = UUID(d.pop("review_id"))

        app_id = UUID(d.pop("app_id"))

        app = d.pop("app")

        job_id = UUID(d.pop("job_id"))

        policy_version = d.pop("policy_version")

        prepared_at = datetime.datetime.fromisoformat(d.pop("prepared_at"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        targets = []
        _targets = d.pop("targets")
        for targets_item_data in _targets:
            targets_item = WorkflowRecoveryPolicyDecision.from_dict(targets_item_data)

            targets.append(targets_item)

        review_url = d.pop("review_url")

        status_url = d.pop("status_url")

        review_command = d.pop("review_command")

        export_command = d.pop("export_command")

        workflow_recovery_review_ready_webhook_payload = cls(
            event_id=event_id,
            review_id=review_id,
            app_id=app_id,
            app=app,
            job_id=job_id,
            policy_version=policy_version,
            prepared_at=prepared_at,
            observed_at=observed_at,
            targets=targets,
            review_url=review_url,
            status_url=status_url,
            review_command=review_command,
            export_command=export_command,
        )

        workflow_recovery_review_ready_webhook_payload.additional_properties = d
        return workflow_recovery_review_ready_webhook_payload

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
