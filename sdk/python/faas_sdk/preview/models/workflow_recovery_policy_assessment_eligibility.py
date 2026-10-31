from typing import Literal

WorkflowRecoveryPolicyAssessmentEligibility = Literal["excluded", "qualifies", "unknown"]

WORKFLOW_RECOVERY_POLICY_ASSESSMENT_ELIGIBILITY_VALUES: set[WorkflowRecoveryPolicyAssessmentEligibility] = {
    "excluded",
    "qualifies",
    "unknown",
}


def check_workflow_recovery_policy_assessment_eligibility(value: str) -> WorkflowRecoveryPolicyAssessmentEligibility:
    if value in WORKFLOW_RECOVERY_POLICY_ASSESSMENT_ELIGIBILITY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_POLICY_ASSESSMENT_ELIGIBILITY_VALUES!r}"
    )
