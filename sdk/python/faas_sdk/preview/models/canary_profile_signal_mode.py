from typing import Literal

CanaryProfileSignalMode = Literal["gate", "report_only"]

CANARY_PROFILE_SIGNAL_MODE_VALUES: set[CanaryProfileSignalMode] = {
    "gate",
    "report_only",
}


def check_canary_profile_signal_mode(value: str) -> CanaryProfileSignalMode:
    if value in CANARY_PROFILE_SIGNAL_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CANARY_PROFILE_SIGNAL_MODE_VALUES!r}")
