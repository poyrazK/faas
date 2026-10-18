from typing import Literal

WorkflowRecoveryPolicyMode = Literal["automatic_initial", "automatic_retry", "preview"]

WORKFLOW_RECOVERY_POLICY_MODE_VALUES: set[WorkflowRecoveryPolicyMode] = {
    "automatic_initial",
    "automatic_retry",
    "preview",
}


def check_workflow_recovery_policy_mode(value: str) -> WorkflowRecoveryPolicyMode:
    if value in WORKFLOW_RECOVERY_POLICY_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_POLICY_MODE_VALUES!r}")
