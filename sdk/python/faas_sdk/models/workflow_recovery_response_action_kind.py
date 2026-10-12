from typing import Literal

WorkflowRecoveryResponseActionKind = Literal["cancel", "retry"]

WORKFLOW_RECOVERY_RESPONSE_ACTION_KIND_VALUES: set[WorkflowRecoveryResponseActionKind] = {
    "cancel",
    "retry",
}


def check_workflow_recovery_response_action_kind(value: str) -> WorkflowRecoveryResponseActionKind:
    if value in WORKFLOW_RECOVERY_RESPONSE_ACTION_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_RESPONSE_ACTION_KIND_VALUES!r}")
