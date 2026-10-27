from typing import Literal

ServiceWakeAheadLearnedSource = Literal["live", "unavailable"]

SERVICE_WAKE_AHEAD_LEARNED_SOURCE_VALUES: set[ServiceWakeAheadLearnedSource] = {
    "live",
    "unavailable",
}


def check_service_wake_ahead_learned_source(value: str) -> ServiceWakeAheadLearnedSource:
    if value in SERVICE_WAKE_AHEAD_LEARNED_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SERVICE_WAKE_AHEAD_LEARNED_SOURCE_VALUES!r}")
