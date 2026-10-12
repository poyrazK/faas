from typing import Literal

WorkflowRecoveryExecutionAttentionTargetStatus = Literal[
    "capacity_wait", "deadline_exceeded", "queued", "running", "scheduled_wait", "stalled", "unknown"
]

WORKFLOW_RECOVERY_EXECUTION_ATTENTION_TARGET_STATUS_VALUES: set[WorkflowRecoveryExecutionAttentionTargetStatus] = {
    "capacity_wait",
    "deadline_exceeded",
    "queued",
    "running",
    "scheduled_wait",
    "stalled",
    "unknown",
}


def check_workflow_recovery_execution_attention_target_status(
    value: str,
) -> WorkflowRecoveryExecutionAttentionTargetStatus:
    if value in WORKFLOW_RECOVERY_EXECUTION_ATTENTION_TARGET_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_EXECUTION_ATTENTION_TARGET_STATUS_VALUES!r}"
    )
