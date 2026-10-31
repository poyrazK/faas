from typing import Literal

ControlWorkflowRecoveryPolicyRequestOperation = Literal["pause", "resume"]

CONTROL_WORKFLOW_RECOVERY_POLICY_REQUEST_OPERATION_VALUES: set[ControlWorkflowRecoveryPolicyRequestOperation] = {
    "pause",
    "resume",
}


def check_control_workflow_recovery_policy_request_operation(
    value: str,
) -> ControlWorkflowRecoveryPolicyRequestOperation:
    if value in CONTROL_WORKFLOW_RECOVERY_POLICY_REQUEST_OPERATION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CONTROL_WORKFLOW_RECOVERY_POLICY_REQUEST_OPERATION_VALUES!r}"
    )
