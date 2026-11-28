from typing import Literal

ObsRuntimePostCaptureHealthState = Literal["empty", "incomplete", "nonempty", "unknown"]

OBS_RUNTIME_POST_CAPTURE_HEALTH_STATE_VALUES: set[ObsRuntimePostCaptureHealthState] = {
    "empty",
    "incomplete",
    "nonempty",
    "unknown",
}


def check_obs_runtime_post_capture_health_state(value: str) -> ObsRuntimePostCaptureHealthState:
    if value in OBS_RUNTIME_POST_CAPTURE_HEALTH_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_POST_CAPTURE_HEALTH_STATE_VALUES!r}")
