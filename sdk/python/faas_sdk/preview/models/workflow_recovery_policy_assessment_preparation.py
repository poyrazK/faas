from typing import Literal

WorkflowRecoveryPolicyAssessmentPreparation = Literal[
    "already_prepared", "cooldown", "excluded", "hourly_budget", "unknown", "would_prepare"
]

WORKFLOW_RECOVERY_POLICY_ASSESSMENT_PREPARATION_VALUES: set[WorkflowRecoveryPolicyAssessmentPreparation] = {
    "already_prepared",
    "cooldown",
    "excluded",
    "hourly_budget",
    "unknown",
    "would_prepare",
}


def check_workflow_recovery_policy_assessment_preparation(value: str) -> WorkflowRecoveryPolicyAssessmentPreparation:
    if value in WORKFLOW_RECOVERY_POLICY_ASSESSMENT_PREPARATION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_POLICY_ASSESSMENT_PREPARATION_VALUES!r}"
    )
