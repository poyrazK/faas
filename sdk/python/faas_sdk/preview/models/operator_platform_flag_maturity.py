from typing import Literal

OperatorPlatformFlagMaturity = Literal["beta", "ga", "internal", "preview"]

OPERATOR_PLATFORM_FLAG_MATURITY_VALUES: set[OperatorPlatformFlagMaturity] = {
    "beta",
    "ga",
    "internal",
    "preview",
}


def check_operator_platform_flag_maturity(value: str) -> OperatorPlatformFlagMaturity:
    if value in OPERATOR_PLATFORM_FLAG_MATURITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATOR_PLATFORM_FLAG_MATURITY_VALUES!r}")
