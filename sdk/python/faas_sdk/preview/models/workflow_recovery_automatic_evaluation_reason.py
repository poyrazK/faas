from typing import Literal

WorkflowRecoveryAutomaticEvaluationReason = Literal[
    "admission_wait",
    "automatic_paused",
    "changed",
    "child_created",
    "cooldown",
    "evidence_unknown",
    "execution_cooldown",
    "execution_quota",
    "execution_wait",
    "expired",
    "hourly_budget",
    "hourly_execution_budget",
    "inspection_error",
    "job_paused",
    "outstanding_recovery",
    "outstanding_retry",
    "policy_changed",
    "policy_disabled",
    "recovery_running",
    "result_recorded",
    "retry_running",
    "suppressed",
    "terminal_outcome_not_saved",
]

WORKFLOW_RECOVERY_AUTOMATIC_EVALUATION_REASON_VALUES: set[WorkflowRecoveryAutomaticEvaluationReason] = {
    "admission_wait",
    "automatic_paused",
    "changed",
    "child_created",
    "cooldown",
    "evidence_unknown",
    "execution_cooldown",
    "execution_quota",
    "execution_wait",
    "expired",
    "hourly_budget",
    "hourly_execution_budget",
    "inspection_error",
    "job_paused",
    "outstanding_recovery",
    "outstanding_retry",
    "policy_changed",
    "policy_disabled",
    "recovery_running",
    "result_recorded",
    "retry_running",
    "suppressed",
    "terminal_outcome_not_saved",
}


def check_workflow_recovery_automatic_evaluation_reason(value: str) -> WorkflowRecoveryAutomaticEvaluationReason:
    if value in WORKFLOW_RECOVERY_AUTOMATIC_EVALUATION_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_AUTOMATIC_EVALUATION_REASON_VALUES!r}"
    )
