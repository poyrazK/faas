from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_policy_evaluation_candidate_evidence_source import (
    WorkflowRecoveryPolicyEvaluationCandidateEvidenceSource,
    check_workflow_recovery_policy_evaluation_candidate_evidence_source,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_recovery_failure_eligibility_assessment import WorkflowRecoveryFailureEligibilityAssessment
    from ..models.workflow_recovery_policy_assessment import WorkflowRecoveryPolicyAssessment


T = TypeVar("T", bound="WorkflowRecoveryPolicyEvaluationCandidate")


@_attrs_define
class WorkflowRecoveryPolicyEvaluationCandidate:
    """One retained historical decision with current and proposed settings applied to the same captured evidence."""

    evidence_source: WorkflowRecoveryPolicyEvaluationCandidateEvidenceSource
    """Source of historical candidate facts."""
    run_id: UUID
    workflow: str
    observed_at: datetime.datetime
    resume_count: int
    trigger: str
    recorded_outcome: str
    historical_failure_statuses: list[str]
    """Exact observed status as a singleton when captured; otherwise the legacy policy filter represents a possible
    status set. Empty means unrestricted and unrecorded."""
    observed_hourly_executions: int
    execution_evidence_ids: list[str]
    """Bounded application proof identifiers from recovery history, including last known cooldown evidence when
    older than one hour."""
    current: WorkflowRecoveryPolicyAssessment
    """Eligibility from preserved facts, conditional preparation result and lower-bound pressure from observed
    applications."""
    proposed: WorkflowRecoveryPolicyAssessment
    """Eligibility from preserved facts, conditional preparation result and lower-bound pressure from observed
    applications."""
    later_eligibility: WorkflowRecoveryFailureEligibilityAssessment | Unset = UNSET
    """Policy filters applied to a separately timed later observation. Checks expire after 15 minutes; preparation
    and execution remain unproven. Historical totals never consume these observations."""
    failure_evidence_id: str | Unset = UNSET
    """Stable identifier for a captured policy-independent failure."""
    candidate_evidence_id: str | Unset = UNSET
    """Durable history event containing the candidate facts when available."""
    review_id: UUID | Unset = UNSET
    job_id: UUID | Unset = UNSET
    policy_version: int | Unset = UNSET
    position: int | Unset = UNSET
    last_observed_execution_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        evidence_source: str = self.evidence_source

        run_id = str(self.run_id)

        workflow = self.workflow

        observed_at = self.observed_at.isoformat()

        resume_count = self.resume_count

        trigger = self.trigger

        recorded_outcome = self.recorded_outcome

        historical_failure_statuses = self.historical_failure_statuses

        observed_hourly_executions = self.observed_hourly_executions

        execution_evidence_ids = self.execution_evidence_ids

        current = self.current.to_dict()

        proposed = self.proposed.to_dict()

        later_eligibility: dict[str, Any] | Unset = UNSET
        if not isinstance(self.later_eligibility, Unset):
            later_eligibility = self.later_eligibility.to_dict()

        failure_evidence_id = self.failure_evidence_id

        candidate_evidence_id = self.candidate_evidence_id

        review_id: str | Unset = UNSET
        if not isinstance(self.review_id, Unset):
            review_id = str(self.review_id)

        job_id: str | Unset = UNSET
        if not isinstance(self.job_id, Unset):
            job_id = str(self.job_id)

        policy_version = self.policy_version

        position = self.position

        last_observed_execution_at: str | Unset = UNSET
        if not isinstance(self.last_observed_execution_at, Unset):
            last_observed_execution_at = self.last_observed_execution_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "evidence_source": evidence_source,
                "run_id": run_id,
                "workflow": workflow,
                "observed_at": observed_at,
                "resume_count": resume_count,
                "trigger": trigger,
                "recorded_outcome": recorded_outcome,
                "historical_failure_statuses": historical_failure_statuses,
                "observed_hourly_executions": observed_hourly_executions,
                "execution_evidence_ids": execution_evidence_ids,
                "current": current,
                "proposed": proposed,
            }
        )
        if later_eligibility is not UNSET:
            field_dict["later_eligibility"] = later_eligibility
        if failure_evidence_id is not UNSET:
            field_dict["failure_evidence_id"] = failure_evidence_id
        if candidate_evidence_id is not UNSET:
            field_dict["candidate_evidence_id"] = candidate_evidence_id
        if review_id is not UNSET:
            field_dict["review_id"] = review_id
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if policy_version is not UNSET:
            field_dict["policy_version"] = policy_version
        if position is not UNSET:
            field_dict["position"] = position
        if last_observed_execution_at is not UNSET:
            field_dict["last_observed_execution_at"] = last_observed_execution_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_failure_eligibility_assessment import (
            WorkflowRecoveryFailureEligibilityAssessment,
        )
        from ..models.workflow_recovery_policy_assessment import WorkflowRecoveryPolicyAssessment

        d = dict(src_dict)
        evidence_source = check_workflow_recovery_policy_evaluation_candidate_evidence_source(d.pop("evidence_source"))

        run_id = UUID(d.pop("run_id"))

        workflow = d.pop("workflow")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        resume_count = d.pop("resume_count")

        trigger = d.pop("trigger")

        recorded_outcome = d.pop("recorded_outcome")

        historical_failure_statuses = cast(list[str], d.pop("historical_failure_statuses"))

        observed_hourly_executions = d.pop("observed_hourly_executions")

        execution_evidence_ids = cast(list[str], d.pop("execution_evidence_ids"))

        current = WorkflowRecoveryPolicyAssessment.from_dict(d.pop("current"))

        proposed = WorkflowRecoveryPolicyAssessment.from_dict(d.pop("proposed"))

        _later_eligibility = d.pop("later_eligibility", UNSET)
        later_eligibility: WorkflowRecoveryFailureEligibilityAssessment | Unset
        if isinstance(_later_eligibility, Unset):
            later_eligibility = UNSET
        else:
            later_eligibility = WorkflowRecoveryFailureEligibilityAssessment.from_dict(_later_eligibility)

        failure_evidence_id = d.pop("failure_evidence_id", UNSET)

        candidate_evidence_id = d.pop("candidate_evidence_id", UNSET)

        _review_id = d.pop("review_id", UNSET)
        review_id: UUID | Unset
        if isinstance(_review_id, Unset):
            review_id = UNSET
        else:
            review_id = UUID(_review_id)

        _job_id = d.pop("job_id", UNSET)
        job_id: UUID | Unset
        if isinstance(_job_id, Unset):
            job_id = UNSET
        else:
            job_id = UUID(_job_id)

        policy_version = d.pop("policy_version", UNSET)

        position = d.pop("position", UNSET)

        _last_observed_execution_at = d.pop("last_observed_execution_at", UNSET)
        last_observed_execution_at: datetime.datetime | Unset
        if isinstance(_last_observed_execution_at, Unset):
            last_observed_execution_at = UNSET
        else:
            last_observed_execution_at = datetime.datetime.fromisoformat(_last_observed_execution_at)

        workflow_recovery_policy_evaluation_candidate = cls(
            evidence_source=evidence_source,
            run_id=run_id,
            workflow=workflow,
            observed_at=observed_at,
            resume_count=resume_count,
            trigger=trigger,
            recorded_outcome=recorded_outcome,
            historical_failure_statuses=historical_failure_statuses,
            observed_hourly_executions=observed_hourly_executions,
            execution_evidence_ids=execution_evidence_ids,
            current=current,
            proposed=proposed,
            later_eligibility=later_eligibility,
            failure_evidence_id=failure_evidence_id,
            candidate_evidence_id=candidate_evidence_id,
            review_id=review_id,
            job_id=job_id,
            policy_version=policy_version,
            position=position,
            last_observed_execution_at=last_observed_execution_at,
        )

        workflow_recovery_policy_evaluation_candidate.additional_properties = d
        return workflow_recovery_policy_evaluation_candidate

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
