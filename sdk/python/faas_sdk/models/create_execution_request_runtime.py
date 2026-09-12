from typing import Literal

CreateExecutionRequestRuntime = Literal["node22", "node24", "python312", "python313"]

CREATE_EXECUTION_REQUEST_RUNTIME_VALUES: set[CreateExecutionRequestRuntime] = {
    "node22",
    "node24",
    "python312",
    "python313",
}


def check_create_execution_request_runtime(value: str) -> CreateExecutionRequestRuntime:
    if value in CREATE_EXECUTION_REQUEST_RUNTIME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_EXECUTION_REQUEST_RUNTIME_VALUES!r}")
