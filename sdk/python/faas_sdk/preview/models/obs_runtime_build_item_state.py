from typing import Literal

ObsRuntimeBuildItemState = Literal["matching", "mismatched", "missing", "stale", "unavailable"]

OBS_RUNTIME_BUILD_ITEM_STATE_VALUES: set[ObsRuntimeBuildItemState] = {
    "matching",
    "mismatched",
    "missing",
    "stale",
    "unavailable",
}


def check_obs_runtime_build_item_state(value: str) -> ObsRuntimeBuildItemState:
    if value in OBS_RUNTIME_BUILD_ITEM_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_BUILD_ITEM_STATE_VALUES!r}")
