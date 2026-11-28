from typing import Literal

WorkflowRecoveryPolicyAssessmentExecutionPressure = Literal[
    "insufficient_history", "not_enabled", "not_evaluated", "observed_cooldown", "observed_hourly_budget"
]

WORKFLOW_RECOVERY_POLICY_ASSESSMENT_EXECUTION_PRESSURE_VALUES: set[
    WorkflowRecoveryPolicyAssessmentExecutionPressure
] = {
    "insufficient_history",
    "not_enabled",
    "not_evaluated",
    "observed_cooldown",
    "observed_hourly_budget",
}


def check_workflow_recovery_policy_assessment_execution_pressure(
    value: str,
) -> WorkflowRecoveryPolicyAssessmentExecutionPressure:
    if value in WORKFLOW_RECOVERY_POLICY_ASSESSMENT_EXECUTION_PRESSURE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_POLICY_ASSESSMENT_EXECUTION_PRESSURE_VALUES!r}"
    )
