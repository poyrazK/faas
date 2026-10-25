from typing import Literal

WorkflowRecoveryAutomaticPollingFailureCode = Literal[
    "invalid_policy_authority", "invalid_polling_data", "invalid_recovery_identity"
]

WORKFLOW_RECOVERY_AUTOMATIC_POLLING_FAILURE_CODE_VALUES: set[WorkflowRecoveryAutomaticPollingFailureCode] = {
    "invalid_policy_authority",
    "invalid_polling_data",
    "invalid_recovery_identity",
}


def check_workflow_recovery_automatic_polling_failure_code(value: str) -> WorkflowRecoveryAutomaticPollingFailureCode:
    if value in WORKFLOW_RECOVERY_AUTOMATIC_POLLING_FAILURE_CODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_AUTOMATIC_POLLING_FAILURE_CODE_VALUES!r}"
    )
