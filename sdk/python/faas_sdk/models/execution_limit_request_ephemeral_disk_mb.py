from typing import Literal

ExecutionLimitRequestEphemeralDiskMb = Literal[0, 64, 128, 256, 512, 1024, 2048]

EXECUTION_LIMIT_REQUEST_EPHEMERAL_DISK_MB_VALUES: set[ExecutionLimitRequestEphemeralDiskMb] = {
    0,
    64,
    128,
    256,
    512,
    1024,
    2048,
}


def check_execution_limit_request_ephemeral_disk_mb(value: int) -> ExecutionLimitRequestEphemeralDiskMb:
    if value in EXECUTION_LIMIT_REQUEST_EPHEMERAL_DISK_MB_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXECUTION_LIMIT_REQUEST_EPHEMERAL_DISK_MB_VALUES!r}")
