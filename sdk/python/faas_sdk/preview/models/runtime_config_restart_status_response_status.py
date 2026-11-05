from typing import Literal

RuntimeConfigRestartStatusResponseStatus = Literal["completed", "failed", "queued", "retrying", "running"]

RUNTIME_CONFIG_RESTART_STATUS_RESPONSE_STATUS_VALUES: set[RuntimeConfigRestartStatusResponseStatus] = {
    "completed",
    "failed",
    "queued",
    "retrying",
    "running",
}


def check_runtime_config_restart_status_response_status(value: str) -> RuntimeConfigRestartStatusResponseStatus:
    if value in RUNTIME_CONFIG_RESTART_STATUS_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {RUNTIME_CONFIG_RESTART_STATUS_RESPONSE_STATUS_VALUES!r}"
    )
