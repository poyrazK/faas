from typing import Literal

OperationWorkflowReadinessVerificationChangeBeforeStatus = Literal[
    "awaiting_verification", "invalidated", "unavailable", "verified"
]

OPERATION_WORKFLOW_READINESS_VERIFICATION_CHANGE_BEFORE_STATUS_VALUES: set[
    OperationWorkflowReadinessVerificationChangeBeforeStatus
] = {
    "awaiting_verification",
    "invalidated",
    "unavailable",
    "verified",
}


def check_operation_workflow_readiness_verification_change_before_status(
    value: str,
) -> OperationWorkflowReadinessVerificationChangeBeforeStatus:
    if value in OPERATION_WORKFLOW_READINESS_VERIFICATION_CHANGE_BEFORE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_VERIFICATION_CHANGE_BEFORE_STATUS_VALUES!r}"
    )
