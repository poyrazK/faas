from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.workflow_recovery_policy import WorkflowRecoveryPolicy
    from ..models.workflow_recovery_policy_decision import WorkflowRecoveryPolicyDecision


T = TypeVar("T", bound="WorkflowRecoveryCandidateEvidence")


@_attrs_define
class WorkflowRecoveryCandidateEvidence:
    """Immutable policy-review candidate facts, captured prospectively without execution inputs or outputs."""

    review_id: UUID
    app_id: UUID
    job_id: UUID
    policy_version: int
    policy: WorkflowRecoveryPolicy
    """Explicit app recovery policy. Preview prepares parent-job plans and worker-discovered initial-failure
    reviews. initial_failure requires explicit failure statuses. Opt-in automatic_retry requires saved_failure only;
    automatic_initial requires initial_failure only. Both require explicit workflow and failure-status filters and
    execution limits. Resume count caps the absolute generation; preparation and execution budgets are separate.
    Cancellation remains manual."""
    observed_at: datetime.datetime
    decisions: list[WorkflowRecoveryPolicyDecision]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        review_id = str(self.review_id)

        app_id = str(self.app_id)

        job_id = str(self.job_id)

        policy_version = self.policy_version

        policy = self.policy.to_dict()

        observed_at = self.observed_at.isoformat()

        decisions = []
        for decisions_item_data in self.decisions:
            decisions_item = decisions_item_data.to_dict()
            decisions.append(decisions_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "review_id": review_id,
                "app_id": app_id,
                "job_id": job_id,
                "policy_version": policy_version,
                "policy": policy,
                "observed_at": observed_at,
                "decisions": decisions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_policy import WorkflowRecoveryPolicy
        from ..models.workflow_recovery_policy_decision import WorkflowRecoveryPolicyDecision

        d = dict(src_dict)
        review_id = UUID(d.pop("review_id"))

        app_id = UUID(d.pop("app_id"))

        job_id = UUID(d.pop("job_id"))

        policy_version = d.pop("policy_version")

        policy = WorkflowRecoveryPolicy.from_dict(d.pop("policy"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        decisions = []
        _decisions = d.pop("decisions")
        for decisions_item_data in _decisions:
            decisions_item = WorkflowRecoveryPolicyDecision.from_dict(decisions_item_data)

            decisions.append(decisions_item)

        workflow_recovery_candidate_evidence = cls(
            review_id=review_id,
            app_id=app_id,
            job_id=job_id,
            policy_version=policy_version,
            policy=policy,
            observed_at=observed_at,
            decisions=decisions,
        )

        workflow_recovery_candidate_evidence.additional_properties = d
        return workflow_recovery_candidate_evidence

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
