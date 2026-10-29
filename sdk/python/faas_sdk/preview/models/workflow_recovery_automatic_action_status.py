from typing import Literal

WorkflowRecoveryAutomaticActionStatus = Literal["applied", "pending", "suppressed"]

WORKFLOW_RECOVERY_AUTOMATIC_ACTION_STATUS_VALUES: set[WorkflowRecoveryAutomaticActionStatus] = {
    "applied",
    "pending",
    "suppressed",
}


def check_workflow_recovery_automatic_action_status(value: str) -> WorkflowRecoveryAutomaticActionStatus:
    if value in WORKFLOW_RECOVERY_AUTOMATIC_ACTION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_AUTOMATIC_ACTION_STATUS_VALUES!r}")
