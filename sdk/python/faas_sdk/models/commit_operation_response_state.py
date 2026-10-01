from typing import Literal

CommitOperationResponseState = Literal["accepted", "cancelled", "completed", "failed", "running", "unknown"]

COMMIT_OPERATION_RESPONSE_STATE_VALUES: set[CommitOperationResponseState] = {
    "accepted",
    "cancelled",
    "completed",
    "failed",
    "running",
    "unknown",
}


def check_commit_operation_response_state(value: str) -> CommitOperationResponseState:
    if value in COMMIT_OPERATION_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {COMMIT_OPERATION_RESPONSE_STATE_VALUES!r}")
