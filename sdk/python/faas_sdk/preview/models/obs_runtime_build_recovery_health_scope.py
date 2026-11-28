from typing import Literal

ObsRuntimeBuildRecoveryHealthScope = Literal["replica_observations_of_shared_fleet"]

OBS_RUNTIME_BUILD_RECOVERY_HEALTH_SCOPE_VALUES: set[ObsRuntimeBuildRecoveryHealthScope] = {
    "replica_observations_of_shared_fleet",
}


def check_obs_runtime_build_recovery_health_scope(value: str) -> ObsRuntimeBuildRecoveryHealthScope:
    if value in OBS_RUNTIME_BUILD_RECOVERY_HEALTH_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_BUILD_RECOVERY_HEALTH_SCOPE_VALUES!r}")
