from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_history_entry_evidence_kind import (
    WorkflowRecoveryHistoryEntryEvidenceKind,
    check_workflow_recovery_history_entry_evidence_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_recovery_candidate_evidence import WorkflowRecoveryCandidateEvidence
    from ..models.workflow_recovery_policy import WorkflowRecoveryPolicy
    from ..models.workflow_recovery_polling_notification import WorkflowRecoveryPollingNotification


T = TypeVar("T", bound="WorkflowRecoveryHistoryEntry")


@_attrs_define
class WorkflowRecoveryHistoryEntry:
    """A retained event or latest observation linked to its available recovery evidence."""

    id: str
    occurred_at: datetime.datetime
    kind: str
    outcome: str
    reason: str
    polling_notification: WorkflowRecoveryPollingNotification | Unset = UNSET
    """Last captured polling alert receipt. queued means durable outbox admission; no_receivers never replays to
    later subscribers. Successful polling retains the recovered receipt separately from the cleared error metadata.
   """
    initial_failure_id: UUID | Unset = UNSET
    evidence_kind: WorkflowRecoveryHistoryEntryEvidenceKind | Unset = UNSET
    resume_count: int | Unset = UNSET
    request_id: UUID | Unset = UNSET
    notification_event_id: UUID | Unset = UNSET
    diagnosis_reason: str | Unset = UNSET
    review_url: str | Unset = UNSET
    evidence_url: str | Unset = UNSET
    job_url: str | Unset = UNSET
    candidate_evidence: WorkflowRecoveryCandidateEvidence | Unset = UNSET
    """Immutable policy-review candidate facts, captured prospectively without execution inputs or outputs."""
    durable: bool | Unset = UNSET
    """True for an immutable event recorded prospectively with a state mutation; absent for retained source
    observations."""
    evaluation_reason: str | Unset = UNSET
    """Worker transition classification; suppression reason remains in reason."""
    episode_id: UUID | Unset = UNSET
    """Health incident episode association preserved across later incidents."""
    policy: WorkflowRecoveryPolicy | Unset = UNSET
    """Explicit app recovery policy. Preview prepares parent-job plans and worker-discovered initial-failure
    reviews. initial_failure requires explicit failure statuses. Opt-in automatic_retry requires saved_failure only;
    automatic_initial requires initial_failure only. Both require explicit workflow and failure-status filters and
    execution limits. Resume count caps the absolute generation; preparation and execution budgets are separate.
    Cancellation remains manual."""
    policy_version: int | Unset = UNSET
    workflow: str | Unset = UNSET
    run_id: str | Unset = UNSET
    review_id: str | Unset = UNSET
    action_id: str | Unset = UNSET
    job_id: str | Unset = UNSET
    child_job_id: str | Unset = UNSET
    actor_kind: str | Unset = UNSET
    actor_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        occurred_at = self.occurred_at.isoformat()

        kind = self.kind

        outcome = self.outcome

        reason = self.reason

        polling_notification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.polling_notification, Unset):
            polling_notification = self.polling_notification.to_dict()

        initial_failure_id: str | Unset = UNSET
        if not isinstance(self.initial_failure_id, Unset):
            initial_failure_id = str(self.initial_failure_id)

        evidence_kind: str | Unset = UNSET
        if not isinstance(self.evidence_kind, Unset):
            evidence_kind = self.evidence_kind

        resume_count = self.resume_count

        request_id: str | Unset = UNSET
        if not isinstance(self.request_id, Unset):
            request_id = str(self.request_id)

        notification_event_id: str | Unset = UNSET
        if not isinstance(self.notification_event_id, Unset):
            notification_event_id = str(self.notification_event_id)

        diagnosis_reason = self.diagnosis_reason

        review_url = self.review_url

        evidence_url = self.evidence_url

        job_url = self.job_url

        candidate_evidence: dict[str, Any] | Unset = UNSET
        if not isinstance(self.candidate_evidence, Unset):
            candidate_evidence = self.candidate_evidence.to_dict()

        durable = self.durable

        evaluation_reason = self.evaluation_reason

        episode_id: str | Unset = UNSET
        if not isinstance(self.episode_id, Unset):
            episode_id = str(self.episode_id)

        policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.policy, Unset):
            policy = self.policy.to_dict()

        policy_version = self.policy_version

        workflow = self.workflow

        run_id = self.run_id

        review_id = self.review_id

        action_id = self.action_id

        job_id = self.job_id

        child_job_id = self.child_job_id

        actor_kind = self.actor_kind

        actor_id = self.actor_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "occurred_at": occurred_at,
                "kind": kind,
                "outcome": outcome,
                "reason": reason,
            }
        )
        if polling_notification is not UNSET:
            field_dict["polling_notification"] = polling_notification
        if initial_failure_id is not UNSET:
            field_dict["initial_failure_id"] = initial_failure_id
        if evidence_kind is not UNSET:
            field_dict["evidence_kind"] = evidence_kind
        if resume_count is not UNSET:
            field_dict["resume_count"] = resume_count
        if request_id is not UNSET:
            field_dict["request_id"] = request_id
        if notification_event_id is not UNSET:
            field_dict["notification_event_id"] = notification_event_id
        if diagnosis_reason is not UNSET:
            field_dict["diagnosis_reason"] = diagnosis_reason
        if review_url is not UNSET:
            field_dict["review_url"] = review_url
        if evidence_url is not UNSET:
            field_dict["evidence_url"] = evidence_url
        if job_url is not UNSET:
            field_dict["job_url"] = job_url
        if candidate_evidence is not UNSET:
            field_dict["candidate_evidence"] = candidate_evidence
        if durable is not UNSET:
            field_dict["durable"] = durable
        if evaluation_reason is not UNSET:
            field_dict["evaluation_reason"] = evaluation_reason
        if episode_id is not UNSET:
            field_dict["episode_id"] = episode_id
        if policy is not UNSET:
            field_dict["policy"] = policy
        if policy_version is not UNSET:
            field_dict["policy_version"] = policy_version
        if workflow is not UNSET:
            field_dict["workflow"] = workflow
        if run_id is not UNSET:
            field_dict["run_id"] = run_id
        if review_id is not UNSET:
            field_dict["review_id"] = review_id
        if action_id is not UNSET:
            field_dict["action_id"] = action_id
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if child_job_id is not UNSET:
            field_dict["child_job_id"] = child_job_id
        if actor_kind is not UNSET:
            field_dict["actor_kind"] = actor_kind
        if actor_id is not UNSET:
            field_dict["actor_id"] = actor_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_candidate_evidence import WorkflowRecoveryCandidateEvidence
        from ..models.workflow_recovery_policy import WorkflowRecoveryPolicy
        from ..models.workflow_recovery_polling_notification import WorkflowRecoveryPollingNotification

        d = dict(src_dict)
        id = d.pop("id")

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        kind = d.pop("kind")

        outcome = d.pop("outcome")

        reason = d.pop("reason")

        _polling_notification = d.pop("polling_notification", UNSET)
        polling_notification: WorkflowRecoveryPollingNotification | Unset
        if isinstance(_polling_notification, Unset):
            polling_notification = UNSET
        else:
            polling_notification = WorkflowRecoveryPollingNotification.from_dict(_polling_notification)

        _initial_failure_id = d.pop("initial_failure_id", UNSET)
        initial_failure_id: UUID | Unset
        if isinstance(_initial_failure_id, Unset):
            initial_failure_id = UNSET
        else:
            initial_failure_id = UUID(_initial_failure_id)

        _evidence_kind = d.pop("evidence_kind", UNSET)
        evidence_kind: WorkflowRecoveryHistoryEntryEvidenceKind | Unset
        if isinstance(_evidence_kind, Unset):
            evidence_kind = UNSET
        else:
            evidence_kind = check_workflow_recovery_history_entry_evidence_kind(_evidence_kind)

        resume_count = d.pop("resume_count", UNSET)

        _request_id = d.pop("request_id", UNSET)
        request_id: UUID | Unset
        if isinstance(_request_id, Unset):
            request_id = UNSET
        else:
            request_id = UUID(_request_id)

        _notification_event_id = d.pop("notification_event_id", UNSET)
        notification_event_id: UUID | Unset
        if isinstance(_notification_event_id, Unset):
            notification_event_id = UNSET
        else:
            notification_event_id = UUID(_notification_event_id)

        diagnosis_reason = d.pop("diagnosis_reason", UNSET)

        review_url = d.pop("review_url", UNSET)

        evidence_url = d.pop("evidence_url", UNSET)

        job_url = d.pop("job_url", UNSET)

        _candidate_evidence = d.pop("candidate_evidence", UNSET)
        candidate_evidence: WorkflowRecoveryCandidateEvidence | Unset
        if isinstance(_candidate_evidence, Unset):
            candidate_evidence = UNSET
        else:
            candidate_evidence = WorkflowRecoveryCandidateEvidence.from_dict(_candidate_evidence)

        durable = d.pop("durable", UNSET)

        evaluation_reason = d.pop("evaluation_reason", UNSET)

        _episode_id = d.pop("episode_id", UNSET)
        episode_id: UUID | Unset
        if isinstance(_episode_id, Unset):
            episode_id = UNSET
        else:
            episode_id = UUID(_episode_id)

        _policy = d.pop("policy", UNSET)
        policy: WorkflowRecoveryPolicy | Unset
        if isinstance(_policy, Unset):
            policy = UNSET
        else:
            policy = WorkflowRecoveryPolicy.from_dict(_policy)

        policy_version = d.pop("policy_version", UNSET)

        workflow = d.pop("workflow", UNSET)

        run_id = d.pop("run_id", UNSET)

        review_id = d.pop("review_id", UNSET)

        action_id = d.pop("action_id", UNSET)

        job_id = d.pop("job_id", UNSET)

        child_job_id = d.pop("child_job_id", UNSET)

        actor_kind = d.pop("actor_kind", UNSET)

        actor_id = d.pop("actor_id", UNSET)

        workflow_recovery_history_entry = cls(
            id=id,
            occurred_at=occurred_at,
            kind=kind,
            outcome=outcome,
            reason=reason,
            polling_notification=polling_notification,
            initial_failure_id=initial_failure_id,
            evidence_kind=evidence_kind,
            resume_count=resume_count,
            request_id=request_id,
            notification_event_id=notification_event_id,
            diagnosis_reason=diagnosis_reason,
            review_url=review_url,
            evidence_url=evidence_url,
            job_url=job_url,
            candidate_evidence=candidate_evidence,
            durable=durable,
            evaluation_reason=evaluation_reason,
            episode_id=episode_id,
            policy=policy,
            policy_version=policy_version,
            workflow=workflow,
            run_id=run_id,
            review_id=review_id,
            action_id=action_id,
            job_id=job_id,
            child_job_id=child_job_id,
            actor_kind=actor_kind,
            actor_id=actor_id,
        )

        workflow_recovery_history_entry.additional_properties = d
        return workflow_recovery_history_entry

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
