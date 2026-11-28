from typing import Literal

ObsRuntimeAppTaskHealthScope = Literal["replica_observations_of_shared_fleet"]

OBS_RUNTIME_APP_TASK_HEALTH_SCOPE_VALUES: set[ObsRuntimeAppTaskHealthScope] = {
    "replica_observations_of_shared_fleet",
}


def check_obs_runtime_app_task_health_scope(value: str) -> ObsRuntimeAppTaskHealthScope:
    if value in OBS_RUNTIME_APP_TASK_HEALTH_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_HEALTH_SCOPE_VALUES!r}")
