from typing import Literal

ObsServingProbeDiagnosticsState = Literal["available", "unknown"]

OBS_SERVING_PROBE_DIAGNOSTICS_STATE_VALUES: set[ObsServingProbeDiagnosticsState] = {
    "available",
    "unknown",
}


def check_obs_serving_probe_diagnostics_state(value: str) -> ObsServingProbeDiagnosticsState:
    if value in OBS_SERVING_PROBE_DIAGNOSTICS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_SERVING_PROBE_DIAGNOSTICS_STATE_VALUES!r}")
