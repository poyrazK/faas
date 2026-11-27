from typing import Literal

ExecutionEnvironmentResponseRuntime = Literal["node22", "node24", "python312", "python313"]

EXECUTION_ENVIRONMENT_RESPONSE_RUNTIME_VALUES: set[ExecutionEnvironmentResponseRuntime] = {
    "node22",
    "node24",
    "python312",
    "python313",
}


def check_execution_environment_response_runtime(value: str) -> ExecutionEnvironmentResponseRuntime:
    if value in EXECUTION_ENVIRONMENT_RESPONSE_RUNTIME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXECUTION_ENVIRONMENT_RESPONSE_RUNTIME_VALUES!r}")
