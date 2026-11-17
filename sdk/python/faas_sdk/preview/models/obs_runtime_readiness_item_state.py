from typing import Literal

ObsRuntimeReadinessItemState = Literal["conflicting", "missing", "not_ready", "ready", "stale", "unavailable"]

OBS_RUNTIME_READINESS_ITEM_STATE_VALUES: set[ObsRuntimeReadinessItemState] = {
    "conflicting",
    "missing",
    "not_ready",
    "ready",
    "stale",
    "unavailable",
}


def check_obs_runtime_readiness_item_state(value: str) -> ObsRuntimeReadinessItemState:
    if value in OBS_RUNTIME_READINESS_ITEM_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_READINESS_ITEM_STATE_VALUES!r}")
