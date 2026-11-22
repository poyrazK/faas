from typing import Literal

ExecutionFileResponseStatus = Literal["deleting", "expired", "failed", "missing", "pending", "ready", "uploading"]

EXECUTION_FILE_RESPONSE_STATUS_VALUES: set[ExecutionFileResponseStatus] = {
    "deleting",
    "expired",
    "failed",
    "missing",
    "pending",
    "ready",
    "uploading",
}


def check_execution_file_response_status(value: str) -> ExecutionFileResponseStatus:
    if value in EXECUTION_FILE_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXECUTION_FILE_RESPONSE_STATUS_VALUES!r}")
