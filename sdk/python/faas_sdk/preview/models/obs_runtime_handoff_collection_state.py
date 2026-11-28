from typing import Literal

ObsRuntimeHandoffCollectionState = Literal["available", "conflicting", "missing", "stale", "unavailable"]

OBS_RUNTIME_HANDOFF_COLLECTION_STATE_VALUES: set[ObsRuntimeHandoffCollectionState] = {
    "available",
    "conflicting",
    "missing",
    "stale",
    "unavailable",
}


def check_obs_runtime_handoff_collection_state(value: str) -> ObsRuntimeHandoffCollectionState:
    if value in OBS_RUNTIME_HANDOFF_COLLECTION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_HANDOFF_COLLECTION_STATE_VALUES!r}")
