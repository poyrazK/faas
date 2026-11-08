from typing import Literal

AppEffectiveLimitsPause = Literal["between_requests", "never", "when_idle"]

APP_EFFECTIVE_LIMITS_PAUSE_VALUES: set[AppEffectiveLimitsPause] = {
    "between_requests",
    "never",
    "when_idle",
}


def check_app_effective_limits_pause(value: str) -> AppEffectiveLimitsPause:
    if value in APP_EFFECTIVE_LIMITS_PAUSE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_EFFECTIVE_LIMITS_PAUSE_VALUES!r}")
