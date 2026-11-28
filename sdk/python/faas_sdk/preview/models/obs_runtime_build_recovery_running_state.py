from typing import Literal

ObsRuntimeBuildRecoveryRunningState = Literal["conflicting", "empty", "missing", "nonempty", "stale", "unavailable"]

OBS_RUNTIME_BUILD_RECOVERY_RUNNING_STATE_VALUES: set[ObsRuntimeBuildRecoveryRunningState] = {
    "conflicting",
    "empty",
    "missing",
    "nonempty",
    "stale",
    "unavailable",
}


def check_obs_runtime_build_recovery_running_state(value: str) -> ObsRuntimeBuildRecoveryRunningState:
    if value in OBS_RUNTIME_BUILD_RECOVERY_RUNNING_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_BUILD_RECOVERY_RUNNING_STATE_VALUES!r}")
