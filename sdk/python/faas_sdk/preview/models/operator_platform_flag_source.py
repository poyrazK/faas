from typing import Literal

OperatorPlatformFlagSource = Literal["default_or_environment", "operator"]

OPERATOR_PLATFORM_FLAG_SOURCE_VALUES: set[OperatorPlatformFlagSource] = {
    "default_or_environment",
    "operator",
}


def check_operator_platform_flag_source(value: str) -> OperatorPlatformFlagSource:
    if value in OPERATOR_PLATFORM_FLAG_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATOR_PLATFORM_FLAG_SOURCE_VALUES!r}")
