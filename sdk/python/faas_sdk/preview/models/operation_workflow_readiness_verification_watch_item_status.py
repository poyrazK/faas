from typing import Literal

OperationWorkflowReadinessVerificationWatchItemStatus = Literal[
    "awaiting_verification", "invalidated", "unavailable", "verified"
]

OPERATION_WORKFLOW_READINESS_VERIFICATION_WATCH_ITEM_STATUS_VALUES: set[
    OperationWorkflowReadinessVerificationWatchItemStatus
] = {
    "awaiting_verification",
    "invalidated",
    "unavailable",
    "verified",
}


def check_operation_workflow_readiness_verification_watch_item_status(
    value: str,
) -> OperationWorkflowReadinessVerificationWatchItemStatus:
    if value in OPERATION_WORKFLOW_READINESS_VERIFICATION_WATCH_ITEM_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_VERIFICATION_WATCH_ITEM_STATUS_VALUES!r}"
    )
