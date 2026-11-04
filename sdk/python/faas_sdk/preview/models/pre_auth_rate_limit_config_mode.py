from typing import Literal

PreAuthRateLimitConfigMode = Literal["enforce", "observe", "off"]

PRE_AUTH_RATE_LIMIT_CONFIG_MODE_VALUES: set[PreAuthRateLimitConfigMode] = {
    "enforce",
    "observe",
    "off",
}


def check_pre_auth_rate_limit_config_mode(value: str) -> PreAuthRateLimitConfigMode:
    if value in PRE_AUTH_RATE_LIMIT_CONFIG_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PRE_AUTH_RATE_LIMIT_CONFIG_MODE_VALUES!r}")
