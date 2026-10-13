from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.workflow_recovery_policy_decision import WorkflowRecoveryPolicyDecision


T = TypeVar("T", bound="WorkflowRecoveryPolicyChangeCandidate")


@_attrs_define
class WorkflowRecoveryPolicyChangeCandidate:
    """Matching retained native target, with preparation eligibility simulated under proposed settings and retained
    deduplication. Would_prepare never creates a plan and does not guarantee execution quota or admission.
    Policy_disabled means an eligible target would not be prepared while disabled.

    """

    job_id: UUID
    decision: WorkflowRecoveryPolicyDecision
    """Frozen per-target explanation including eligibility refusals and policy suppression. The resume count
    identifies the reviewed admitted generation."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        job_id = str(self.job_id)

        decision = self.decision.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "job_id": job_id,
                "decision": decision,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_policy_decision import WorkflowRecoveryPolicyDecision

        d = dict(src_dict)
        job_id = UUID(d.pop("job_id"))

        decision = WorkflowRecoveryPolicyDecision.from_dict(d.pop("decision"))

        workflow_recovery_policy_change_candidate = cls(
            job_id=job_id,
            decision=decision,
        )

        workflow_recovery_policy_change_candidate.additional_properties = d
        return workflow_recovery_policy_change_candidate

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
