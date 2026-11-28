from typing import Literal

ObsRuntimeBuilderSlotsState = Literal["available", "conflicting", "missing", "stale", "unavailable"]

OBS_RUNTIME_BUILDER_SLOTS_STATE_VALUES: set[ObsRuntimeBuilderSlotsState] = {
    "available",
    "conflicting",
    "missing",
    "stale",
    "unavailable",
}


def check_obs_runtime_builder_slots_state(value: str) -> ObsRuntimeBuilderSlotsState:
    if value in OBS_RUNTIME_BUILDER_SLOTS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_BUILDER_SLOTS_STATE_VALUES!r}")
