from typing import Literal

ExecutionLimitRequestMemoryMb = Literal[0, 128, 256, 512, 1024]

EXECUTION_LIMIT_REQUEST_MEMORY_MB_VALUES: set[ExecutionLimitRequestMemoryMb] = {
    0,
    128,
    256,
    512,
    1024,
}


def check_execution_limit_request_memory_mb(value: int) -> ExecutionLimitRequestMemoryMb:
    if value in EXECUTION_LIMIT_REQUEST_MEMORY_MB_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXECUTION_LIMIT_REQUEST_MEMORY_MB_VALUES!r}")
