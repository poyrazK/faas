from typing import Literal

ObsRuntimePreparationItemState = Literal["available", "conflicting", "missing", "stale", "unavailable"]

OBS_RUNTIME_PREPARATION_ITEM_STATE_VALUES: set[ObsRuntimePreparationItemState] = {
    "available",
    "conflicting",
    "missing",
    "stale",
    "unavailable",
}


def check_obs_runtime_preparation_item_state(value: str) -> ObsRuntimePreparationItemState:
    if value in OBS_RUNTIME_PREPARATION_ITEM_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_PREPARATION_ITEM_STATE_VALUES!r}")
