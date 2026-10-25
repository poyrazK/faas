from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_failure_eligibility_assessment_freshness import (
    WorkflowRecoveryFailureEligibilityAssessmentFreshness,
    check_workflow_recovery_failure_eligibility_assessment_freshness,
)

if TYPE_CHECKING:
    from ..models.workflow_recovery_failure_eligibility import WorkflowRecoveryFailureEligibility
    from ..models.workflow_recovery_policy_assessment import WorkflowRecoveryPolicyAssessment


T = TypeVar("T", bound="WorkflowRecoveryFailureEligibilityAssessment")


@_attrs_define
class WorkflowRecoveryFailureEligibilityAssessment:
    """Policy filters applied to a separately timed later observation. Checks expire after 15 minutes; preparation and
    execution remain unproven. Historical totals never consume these observations.

    """

    observation: WorkflowRecoveryFailureEligibility
    """Latest retained-state retry checks, observed after the original failure. No inputs or outputs are retained;
    this is not failure-time proof or an admission guarantee."""
    freshness: WorkflowRecoveryFailureEligibilityAssessmentFreshness
    current: WorkflowRecoveryPolicyAssessment
    """Eligibility from preserved facts, conditional preparation result and lower-bound pressure from observed
    applications."""
    proposed: WorkflowRecoveryPolicyAssessment
    """Eligibility from preserved facts, conditional preparation result and lower-bound pressure from observed
    applications."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        observation = self.observation.to_dict()

        freshness: str = self.freshness

        current = self.current.to_dict()

        proposed = self.proposed.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "observation": observation,
                "freshness": freshness,
                "current": current,
                "proposed": proposed,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_failure_eligibility import WorkflowRecoveryFailureEligibility
        from ..models.workflow_recovery_policy_assessment import WorkflowRecoveryPolicyAssessment

        d = dict(src_dict)
        observation = WorkflowRecoveryFailureEligibility.from_dict(d.pop("observation"))

        freshness = check_workflow_recovery_failure_eligibility_assessment_freshness(d.pop("freshness"))

        current = WorkflowRecoveryPolicyAssessment.from_dict(d.pop("current"))

        proposed = WorkflowRecoveryPolicyAssessment.from_dict(d.pop("proposed"))

        workflow_recovery_failure_eligibility_assessment = cls(
            observation=observation,
            freshness=freshness,
            current=current,
            proposed=proposed,
        )

        workflow_recovery_failure_eligibility_assessment.additional_properties = d
        return workflow_recovery_failure_eligibility_assessment

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
