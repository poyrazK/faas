from typing import Literal

ObsRuntimePostCaptureObservationState = Literal["available", "conflicting", "missing", "stale", "unavailable"]

OBS_RUNTIME_POST_CAPTURE_OBSERVATION_STATE_VALUES: set[ObsRuntimePostCaptureObservationState] = {
    "available",
    "conflicting",
    "missing",
    "stale",
    "unavailable",
}


def check_obs_runtime_post_capture_observation_state(value: str) -> ObsRuntimePostCaptureObservationState:
    if value in OBS_RUNTIME_POST_CAPTURE_OBSERVATION_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_POST_CAPTURE_OBSERVATION_STATE_VALUES!r}"
    )
