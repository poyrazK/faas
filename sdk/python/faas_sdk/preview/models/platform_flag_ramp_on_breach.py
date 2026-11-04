from typing import Literal

PlatformFlagRampOnBreach = Literal["pause", "rollback"]

PLATFORM_FLAG_RAMP_ON_BREACH_VALUES: set[PlatformFlagRampOnBreach] = {
    "pause",
    "rollback",
}


def check_platform_flag_ramp_on_breach(value: str) -> PlatformFlagRampOnBreach:
    if value in PLATFORM_FLAG_RAMP_ON_BREACH_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLATFORM_FLAG_RAMP_ON_BREACH_VALUES!r}")
