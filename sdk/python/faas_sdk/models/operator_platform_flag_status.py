from typing import Literal

OperatorPlatformFlagStatus = Literal["applied", "blocked", "failed", "pending"]

OPERATOR_PLATFORM_FLAG_STATUS_VALUES: set[OperatorPlatformFlagStatus] = {
    "applied",
    "blocked",
    "failed",
    "pending",
}


def check_operator_platform_flag_status(value: str) -> OperatorPlatformFlagStatus:
    if value in OPERATOR_PLATFORM_FLAG_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATOR_PLATFORM_FLAG_STATUS_VALUES!r}")
