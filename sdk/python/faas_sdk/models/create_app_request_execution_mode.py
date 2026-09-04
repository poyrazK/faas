from typing import Literal

CreateAppRequestExecutionMode = Literal["job", "request", "service", "worker"]

CREATE_APP_REQUEST_EXECUTION_MODE_VALUES: set[CreateAppRequestExecutionMode] = {
    "job",
    "request",
    "service",
    "worker",
}


def check_create_app_request_execution_mode(value: str) -> CreateAppRequestExecutionMode:
    if value in CREATE_APP_REQUEST_EXECUTION_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_APP_REQUEST_EXECUTION_MODE_VALUES!r}")
