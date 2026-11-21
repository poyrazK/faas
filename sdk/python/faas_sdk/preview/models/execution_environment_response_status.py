from typing import Literal

ExecutionEnvironmentResponseStatus = Literal[
    "building", "built", "deleted", "failed", "imaging", "queued", "ready", "retiring"
]

EXECUTION_ENVIRONMENT_RESPONSE_STATUS_VALUES: set[ExecutionEnvironmentResponseStatus] = {
    "building",
    "built",
    "deleted",
    "failed",
    "imaging",
    "queued",
    "ready",
    "retiring",
}


def check_execution_environment_response_status(value: str) -> ExecutionEnvironmentResponseStatus:
    if value in EXECUTION_ENVIRONMENT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXECUTION_ENVIRONMENT_RESPONSE_STATUS_VALUES!r}")
