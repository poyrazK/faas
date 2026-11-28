from typing import Literal

WorkflowRecoveryPolicyResponseStatus = Literal["disabled", "monitoring", "paused", "review_limit"]

WORKFLOW_RECOVERY_POLICY_RESPONSE_STATUS_VALUES: set[WorkflowRecoveryPolicyResponseStatus] = {
    "disabled",
    "monitoring",
    "paused",
    "review_limit",
}


def check_workflow_recovery_policy_response_status(value: str) -> WorkflowRecoveryPolicyResponseStatus:
    if value in WORKFLOW_RECOVERY_POLICY_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_POLICY_RESPONSE_STATUS_VALUES!r}")
