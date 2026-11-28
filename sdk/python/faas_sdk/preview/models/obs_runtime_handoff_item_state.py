from typing import Literal

ObsRuntimeHandoffItemState = Literal["available", "conflicting", "missing", "stale", "unavailable"]

OBS_RUNTIME_HANDOFF_ITEM_STATE_VALUES: set[ObsRuntimeHandoffItemState] = {
    "available",
    "conflicting",
    "missing",
    "stale",
    "unavailable",
}


def check_obs_runtime_handoff_item_state(value: str) -> ObsRuntimeHandoffItemState:
    if value in OBS_RUNTIME_HANDOFF_ITEM_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_HANDOFF_ITEM_STATE_VALUES!r}")
