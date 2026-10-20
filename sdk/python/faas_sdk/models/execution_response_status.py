from typing import Literal

ExecutionResponseStatus = Literal[
    "cancelled", "failed", "out_of_memory", "queued", "restoring", "running", "succeeded", "timed_out"
]

EXECUTION_RESPONSE_STATUS_VALUES: set[ExecutionResponseStatus] = {
    "cancelled",
    "failed",
    "out_of_memory",
    "queued",
    "restoring",
    "running",
    "succeeded",
    "timed_out",
}


def check_execution_response_status(value: str) -> ExecutionResponseStatus:
    if value in EXECUTION_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXECUTION_RESPONSE_STATUS_VALUES!r}")
