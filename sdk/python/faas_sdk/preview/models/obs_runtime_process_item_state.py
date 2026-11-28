from typing import Literal

ObsRuntimeProcessItemState = Literal["conflicting", "fresh", "missing", "stale", "unavailable"]

OBS_RUNTIME_PROCESS_ITEM_STATE_VALUES: set[ObsRuntimeProcessItemState] = {
    "conflicting",
    "fresh",
    "missing",
    "stale",
    "unavailable",
}


def check_obs_runtime_process_item_state(value: str) -> ObsRuntimeProcessItemState:
    if value in OBS_RUNTIME_PROCESS_ITEM_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_PROCESS_ITEM_STATE_VALUES!r}")
