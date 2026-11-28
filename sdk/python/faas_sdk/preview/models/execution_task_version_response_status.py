from typing import Literal

ExecutionTaskVersionResponseStatus = Literal["active", "retired"]

EXECUTION_TASK_VERSION_RESPONSE_STATUS_VALUES: set[ExecutionTaskVersionResponseStatus] = {
    "active",
    "retired",
}


def check_execution_task_version_response_status(value: str) -> ExecutionTaskVersionResponseStatus:
    if value in EXECUTION_TASK_VERSION_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXECUTION_TASK_VERSION_RESPONSE_STATUS_VALUES!r}")
