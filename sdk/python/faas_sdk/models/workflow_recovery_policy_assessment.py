from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_policy_assessment_eligibility import (
    WorkflowRecoveryPolicyAssessmentEligibility,
    check_workflow_recovery_policy_assessment_eligibility,
)
from ..models.workflow_recovery_policy_assessment_execution_pressure import (
    WorkflowRecoveryPolicyAssessmentExecutionPressure,
    check_workflow_recovery_policy_assessment_execution_pressure,
)
from ..models.workflow_recovery_policy_assessment_preparation import (
    WorkflowRecoveryPolicyAssessmentPreparation,
    check_workflow_recovery_policy_assessment_preparation,
)

T = TypeVar("T", bound="WorkflowRecoveryPolicyAssessment")


@_attrs_define
class WorkflowRecoveryPolicyAssessment:
    """Eligibility from preserved facts, conditional preparation result and lower-bound pressure from observed
    applications.

    """

    eligibility: WorkflowRecoveryPolicyAssessmentEligibility
    reason: str
    preparation: WorkflowRecoveryPolicyAssessmentPreparation
    execution_pressure: WorkflowRecoveryPolicyAssessmentExecutionPressure
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        eligibility: str = self.eligibility

        reason = self.reason

        preparation: str = self.preparation

        execution_pressure: str = self.execution_pressure

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "eligibility": eligibility,
                "reason": reason,
                "preparation": preparation,
                "execution_pressure": execution_pressure,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        eligibility = check_workflow_recovery_policy_assessment_eligibility(d.pop("eligibility"))

        reason = d.pop("reason")

        preparation = check_workflow_recovery_policy_assessment_preparation(d.pop("preparation"))

        execution_pressure = check_workflow_recovery_policy_assessment_execution_pressure(d.pop("execution_pressure"))

        workflow_recovery_policy_assessment = cls(
            eligibility=eligibility,
            reason=reason,
            preparation=preparation,
            execution_pressure=execution_pressure,
        )

        workflow_recovery_policy_assessment.additional_properties = d
        return workflow_recovery_policy_assessment

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
