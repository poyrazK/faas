from typing import Literal

RollbackOperationStatus = Literal["blocked", "complete", "failed", "preparing", "ready", "routing"]

ROLLBACK_OPERATION_STATUS_VALUES: set[RollbackOperationStatus] = {
    "blocked",
    "complete",
    "failed",
    "preparing",
    "ready",
    "routing",
}


def check_rollback_operation_status(value: str) -> RollbackOperationStatus:
    if value in ROLLBACK_OPERATION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROLLBACK_OPERATION_STATUS_VALUES!r}")
