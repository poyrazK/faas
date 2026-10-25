from typing import Literal

ObsHealthSignalReason = Literal[
    "insufficient_samples",
    "invalid_observation",
    "invalid_value",
    "missing_observation",
    "not_configured",
    "observation_in_future",
    "observation_stale",
    "query_failed",
]

OBS_HEALTH_SIGNAL_REASON_VALUES: set[ObsHealthSignalReason] = {
    "insufficient_samples",
    "invalid_observation",
    "invalid_value",
    "missing_observation",
    "not_configured",
    "observation_in_future",
    "observation_stale",
    "query_failed",
}


def check_obs_health_signal_reason(value: str) -> ObsHealthSignalReason:
    if value in OBS_HEALTH_SIGNAL_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_HEALTH_SIGNAL_REASON_VALUES!r}")
