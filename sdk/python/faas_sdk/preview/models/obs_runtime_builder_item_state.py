from typing import Literal

ObsRuntimeBuilderItemState = Literal["available", "conflicting", "missing", "stale", "unavailable"]

OBS_RUNTIME_BUILDER_ITEM_STATE_VALUES: set[ObsRuntimeBuilderItemState] = {
    "available",
    "conflicting",
    "missing",
    "stale",
    "unavailable",
}


def check_obs_runtime_builder_item_state(value: str) -> ObsRuntimeBuilderItemState:
    if value in OBS_RUNTIME_BUILDER_ITEM_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_BUILDER_ITEM_STATE_VALUES!r}")
