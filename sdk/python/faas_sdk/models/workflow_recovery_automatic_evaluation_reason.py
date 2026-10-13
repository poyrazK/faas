from typing import Literal

WorkflowRecoveryAutomaticEvaluationReason = Literal[
    "admission_wait",
    "child_created",
    "cooldown",
    "evidence_unknown",
    "execution_quota",
    "execution_wait",
    "hourly_budget",
    "inspection_error",
    "outstanding_retry",
    "result_recorded",
    "retry_running",
    "suppressed",
]

WORKFLOW_RECOVERY_AUTOMATIC_EVALUATION_REASON_VALUES: set[WorkflowRecoveryAutomaticEvaluationReason] = {
    "admission_wait",
    "child_created",
    "cooldown",
    "evidence_unknown",
    "execution_quota",
    "execution_wait",
    "hourly_budget",
    "inspection_error",
    "outstanding_retry",
    "result_recorded",
    "retry_running",
    "suppressed",
}


def check_workflow_recovery_automatic_evaluation_reason(value: str) -> WorkflowRecoveryAutomaticEvaluationReason:
    if value in WORKFLOW_RECOVERY_AUTOMATIC_EVALUATION_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_AUTOMATIC_EVALUATION_REASON_VALUES!r}"
    )
