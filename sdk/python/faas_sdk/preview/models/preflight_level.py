from typing import Literal

PreflightLevel = Literal["amber", "green", "red"]

PREFLIGHT_LEVEL_VALUES: set[PreflightLevel] = {
    "amber",
    "green",
    "red",
}


def check_preflight_level(value: str) -> PreflightLevel:
    if value in PREFLIGHT_LEVEL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PREFLIGHT_LEVEL_VALUES!r}")
