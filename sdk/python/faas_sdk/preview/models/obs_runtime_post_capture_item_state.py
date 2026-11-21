from typing import Literal

ObsRuntimePostCaptureItemState = Literal["available", "conflicting", "missing", "stale", "unavailable"]

OBS_RUNTIME_POST_CAPTURE_ITEM_STATE_VALUES: set[ObsRuntimePostCaptureItemState] = {
    "available",
    "conflicting",
    "missing",
    "stale",
    "unavailable",
}


def check_obs_runtime_post_capture_item_state(value: str) -> ObsRuntimePostCaptureItemState:
    if value in OBS_RUNTIME_POST_CAPTURE_ITEM_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_POST_CAPTURE_ITEM_STATE_VALUES!r}")
