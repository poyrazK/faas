from typing import Literal

ExecutionResponseRuntime = Literal["node22", "node24", "python312", "python313"]

EXECUTION_RESPONSE_RUNTIME_VALUES: set[ExecutionResponseRuntime] = {
    "node22",
    "node24",
    "python312",
    "python313",
}


def check_execution_response_runtime(value: str) -> ExecutionResponseRuntime:
    if value in EXECUTION_RESPONSE_RUNTIME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXECUTION_RESPONSE_RUNTIME_VALUES!r}")
