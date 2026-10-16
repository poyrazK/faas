from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_queue_blocked_webhook_payload_evidence_kind import (
    WorkflowRecoveryQueueBlockedWebhookPayloadEvidenceKind,
    check_workflow_recovery_queue_blocked_webhook_payload_evidence_kind,
)
from ..models.workflow_recovery_queue_blocked_webhook_payload_reason import (
    WorkflowRecoveryQueueBlockedWebhookPayloadReason,
    check_workflow_recovery_queue_blocked_webhook_payload_reason,
)
from ..models.workflow_recovery_queue_blocked_webhook_payload_state import (
    WorkflowRecoveryQueueBlockedWebhookPayloadState,
    check_workflow_recovery_queue_blocked_webhook_payload_state,
)
from ..models.workflow_recovery_queue_blocked_webhook_payload_status import (
    WorkflowRecoveryQueueBlockedWebhookPayloadStatus,
    check_workflow_recovery_queue_blocked_webhook_payload_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowRecoveryQueueBlockedWebhookPayload")


@_attrs_define
class WorkflowRecoveryQueueBlockedWebhookPayload:
    """Historical automation.recovery_queue_blocked event. Saved-failure retries capture changes in exact parent or child
    evidence diagnosis and proven clearance, with diagnosis URL and CLI guidance. Initial automatic recovery identifies
    initial_failure_id. Unchanged blockers stay quiet; recovery identifies cleared evidence rather than successful
    execution. Capturing this event does not finalize a child, release its execution slot, or authorize another retry.
    Delivery may occur after the block resolves; stable identity and frozen recipients support receiver deduplication.

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
    evidence_kind: WorkflowRecoveryQueueBlockedWebhookPayloadEvidenceKind | Unset = UNSET
    diagnosis_reason: str | Unset = UNSET
    evidence_url: str | Unset = UNSET
    run_id: UUID | Unset = UNSET
    resume_count: int | Unset = UNSET
    repairable: bool | Unset = UNSET
    cli_command: str | Unset = UNSET
    initial_failure_id: UUID | Unset = UNSET
    state: WorkflowRecoveryQueueBlockedWebhookPayloadState | Unset = UNSET
    previous_reason: str | Unset = UNSET
    operator_action: str | Unset = UNSET
    job_url: str | Unset = UNSET
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

        evidence_kind: str | Unset = UNSET
        if not isinstance(self.evidence_kind, Unset):
            evidence_kind = self.evidence_kind

        diagnosis_reason = self.diagnosis_reason

        evidence_url = self.evidence_url

        run_id: str | Unset = UNSET
        if not isinstance(self.run_id, Unset):
            run_id = str(self.run_id)

        resume_count = self.resume_count

        repairable = self.repairable

        cli_command = self.cli_command

        initial_failure_id: str | Unset = UNSET
        if not isinstance(self.initial_failure_id, Unset):
            initial_failure_id = str(self.initial_failure_id)

        state: str | Unset = UNSET
        if not isinstance(self.state, Unset):
            state = self.state

        previous_reason = self.previous_reason

        operator_action = self.operator_action

        job_url = self.job_url

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
        if evidence_kind is not UNSET:
            field_dict["evidence_kind"] = evidence_kind
        if diagnosis_reason is not UNSET:
            field_dict["diagnosis_reason"] = diagnosis_reason
        if evidence_url is not UNSET:
            field_dict["evidence_url"] = evidence_url
        if run_id is not UNSET:
            field_dict["run_id"] = run_id
        if resume_count is not UNSET:
            field_dict["resume_count"] = resume_count
        if repairable is not UNSET:
            field_dict["repairable"] = repairable
        if cli_command is not UNSET:
            field_dict["cli_command"] = cli_command
        if initial_failure_id is not UNSET:
            field_dict["initial_failure_id"] = initial_failure_id
        if state is not UNSET:
            field_dict["state"] = state
        if previous_reason is not UNSET:
            field_dict["previous_reason"] = previous_reason
        if operator_action is not UNSET:
            field_dict["operator_action"] = operator_action
        if job_url is not UNSET:
            field_dict["job_url"] = job_url
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

        _evidence_kind = d.pop("evidence_kind", UNSET)
        evidence_kind: WorkflowRecoveryQueueBlockedWebhookPayloadEvidenceKind | Unset
        if isinstance(_evidence_kind, Unset):
            evidence_kind = UNSET
        else:
            evidence_kind = check_workflow_recovery_queue_blocked_webhook_payload_evidence_kind(_evidence_kind)

        diagnosis_reason = d.pop("diagnosis_reason", UNSET)

        evidence_url = d.pop("evidence_url", UNSET)

        _run_id = d.pop("run_id", UNSET)
        run_id: UUID | Unset
        if isinstance(_run_id, Unset):
            run_id = UNSET
        else:
            run_id = UUID(_run_id)

        resume_count = d.pop("resume_count", UNSET)

        repairable = d.pop("repairable", UNSET)

        cli_command = d.pop("cli_command", UNSET)

        _initial_failure_id = d.pop("initial_failure_id", UNSET)
        initial_failure_id: UUID | Unset
        if isinstance(_initial_failure_id, Unset):
            initial_failure_id = UNSET
        else:
            initial_failure_id = UUID(_initial_failure_id)

        _state = d.pop("state", UNSET)
        state: WorkflowRecoveryQueueBlockedWebhookPayloadState | Unset
        if isinstance(_state, Unset):
            state = UNSET
        else:
            state = check_workflow_recovery_queue_blocked_webhook_payload_state(_state)

        previous_reason = d.pop("previous_reason", UNSET)

        operator_action = d.pop("operator_action", UNSET)

        job_url = d.pop("job_url", UNSET)

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
            evidence_kind=evidence_kind,
            diagnosis_reason=diagnosis_reason,
            evidence_url=evidence_url,
            run_id=run_id,
            resume_count=resume_count,
            repairable=repairable,
            cli_command=cli_command,
            initial_failure_id=initial_failure_id,
            state=state,
            previous_reason=previous_reason,
            operator_action=operator_action,
            job_url=job_url,
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
