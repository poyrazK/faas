from typing import Literal

ObsRuntimeBuildsScope = Literal["selected_critical_daemon_builds"]

OBS_RUNTIME_BUILDS_SCOPE_VALUES: set[ObsRuntimeBuildsScope] = {
    "selected_critical_daemon_builds",
}


def check_obs_runtime_builds_scope(value: str) -> ObsRuntimeBuildsScope:
    if value in OBS_RUNTIME_BUILDS_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_BUILDS_SCOPE_VALUES!r}")
