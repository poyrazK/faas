from typing import Literal

ObsRuntimeBuildRecoveryHealthState = Literal["empty", "incomplete", "nonempty", "unknown"]

OBS_RUNTIME_BUILD_RECOVERY_HEALTH_STATE_VALUES: set[ObsRuntimeBuildRecoveryHealthState] = {
    "empty",
    "incomplete",
    "nonempty",
    "unknown",
}


def check_obs_runtime_build_recovery_health_state(value: str) -> ObsRuntimeBuildRecoveryHealthState:
    if value in OBS_RUNTIME_BUILD_RECOVERY_HEALTH_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_BUILD_RECOVERY_HEALTH_STATE_VALUES!r}")
