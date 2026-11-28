from typing import Literal

ObsRuntimePostCaptureClassState = Literal["conflicting", "empty", "missing", "nonempty", "stale", "unavailable"]

OBS_RUNTIME_POST_CAPTURE_CLASS_STATE_VALUES: set[ObsRuntimePostCaptureClassState] = {
    "conflicting",
    "empty",
    "missing",
    "nonempty",
    "stale",
    "unavailable",
}


def check_obs_runtime_post_capture_class_state(value: str) -> ObsRuntimePostCaptureClassState:
    if value in OBS_RUNTIME_POST_CAPTURE_CLASS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_POST_CAPTURE_CLASS_STATE_VALUES!r}")
