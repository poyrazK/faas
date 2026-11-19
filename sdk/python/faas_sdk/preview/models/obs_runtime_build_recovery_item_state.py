from typing import Literal

ObsRuntimeBuildRecoveryItemState = Literal["available", "conflicting", "missing", "stale", "unavailable"]

OBS_RUNTIME_BUILD_RECOVERY_ITEM_STATE_VALUES: set[ObsRuntimeBuildRecoveryItemState] = {
    "available",
    "conflicting",
    "missing",
    "stale",
    "unavailable",
}


def check_obs_runtime_build_recovery_item_state(value: str) -> ObsRuntimeBuildRecoveryItemState:
    if value in OBS_RUNTIME_BUILD_RECOVERY_ITEM_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_BUILD_RECOVERY_ITEM_STATE_VALUES!r}")
