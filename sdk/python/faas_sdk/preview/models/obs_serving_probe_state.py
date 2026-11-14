from typing import Literal

ObsServingProbeState = Literal[
    "budget_mismatch", "content_disabled", "failing", "healthy", "invalid_telemetry", "missing", "stale", "unknown"
]

OBS_SERVING_PROBE_STATE_VALUES: set[ObsServingProbeState] = {
    "budget_mismatch",
    "content_disabled",
    "failing",
    "healthy",
    "invalid_telemetry",
    "missing",
    "stale",
    "unknown",
}


def check_obs_serving_probe_state(value: str) -> ObsServingProbeState:
    if value in OBS_SERVING_PROBE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_SERVING_PROBE_STATE_VALUES!r}")
