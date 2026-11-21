from typing import Literal

ExecutionLimitRequestExecutionClass = Literal["extended", "standard"]

EXECUTION_LIMIT_REQUEST_EXECUTION_CLASS_VALUES: set[ExecutionLimitRequestExecutionClass] = {
    "extended",
    "standard",
}


def check_execution_limit_request_execution_class(value: str) -> ExecutionLimitRequestExecutionClass:
    if value in EXECUTION_LIMIT_REQUEST_EXECUTION_CLASS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXECUTION_LIMIT_REQUEST_EXECUTION_CLASS_VALUES!r}")
