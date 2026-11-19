from typing import Literal

ObsRuntimeLoopItemState = Literal["conflicting", "missing", "no_reported_stall", "stale", "stalled", "unavailable"]

OBS_RUNTIME_LOOP_ITEM_STATE_VALUES: set[ObsRuntimeLoopItemState] = {
    "conflicting",
    "missing",
    "no_reported_stall",
    "stale",
    "stalled",
    "unavailable",
}


def check_obs_runtime_loop_item_state(value: str) -> ObsRuntimeLoopItemState:
    if value in OBS_RUNTIME_LOOP_ITEM_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_LOOP_ITEM_STATE_VALUES!r}")
