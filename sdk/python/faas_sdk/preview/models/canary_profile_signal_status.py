from typing import Literal

CanaryProfileSignalStatus = Literal[
    "cancelled", "inconclusive", "no_regression_detected", "queued", "regressed", "running"
]

CANARY_PROFILE_SIGNAL_STATUS_VALUES: set[CanaryProfileSignalStatus] = {
    "cancelled",
    "inconclusive",
    "no_regression_detected",
    "queued",
    "regressed",
    "running",
}


def check_canary_profile_signal_status(value: str) -> CanaryProfileSignalStatus:
    if value in CANARY_PROFILE_SIGNAL_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CANARY_PROFILE_SIGNAL_STATUS_VALUES!r}")
