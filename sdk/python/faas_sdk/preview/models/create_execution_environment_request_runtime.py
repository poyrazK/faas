from typing import Literal

CreateExecutionEnvironmentRequestRuntime = Literal["node22", "node24", "python312", "python313"]

CREATE_EXECUTION_ENVIRONMENT_REQUEST_RUNTIME_VALUES: set[CreateExecutionEnvironmentRequestRuntime] = {
    "node22",
    "node24",
    "python312",
    "python313",
}


def check_create_execution_environment_request_runtime(value: str) -> CreateExecutionEnvironmentRequestRuntime:
    if value in CREATE_EXECUTION_ENVIRONMENT_REQUEST_RUNTIME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_EXECUTION_ENVIRONMENT_REQUEST_RUNTIME_VALUES!r}"
    )
