from typing import Literal

ObsRuntimeBuildRecoveryCleanupState = Literal["conflicting", "empty", "missing", "nonempty", "stale", "unavailable"]

OBS_RUNTIME_BUILD_RECOVERY_CLEANUP_STATE_VALUES: set[ObsRuntimeBuildRecoveryCleanupState] = {
    "conflicting",
    "empty",
    "missing",
    "nonempty",
    "stale",
    "unavailable",
}


def check_obs_runtime_build_recovery_cleanup_state(value: str) -> ObsRuntimeBuildRecoveryCleanupState:
    if value in OBS_RUNTIME_BUILD_RECOVERY_CLEANUP_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_BUILD_RECOVERY_CLEANUP_STATE_VALUES!r}")
