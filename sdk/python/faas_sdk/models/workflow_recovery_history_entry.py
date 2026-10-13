from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_recovery_candidate_evidence import WorkflowRecoveryCandidateEvidence
    from ..models.workflow_recovery_policy import WorkflowRecoveryPolicy


T = TypeVar("T", bound="WorkflowRecoveryHistoryEntry")


@_attrs_define
class WorkflowRecoveryHistoryEntry:
    """A retained event or latest observation linked to its available recovery evidence."""

    id: str
    occurred_at: datetime.datetime
    kind: str
    outcome: str
    reason: str
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
    """Explicit app recovery policy. Preview prepares plans only. Opt-in automatic_retry requires saved_failure
    only and explicit failure statuses and execution limits. Resume count caps the absolute generation; preparation
    and execution budgets are separate. Cancellation remains manual."""
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

        d = dict(src_dict)
        id = d.pop("id")

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        kind = d.pop("kind")

        outcome = d.pop("outcome")

        reason = d.pop("reason")

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
