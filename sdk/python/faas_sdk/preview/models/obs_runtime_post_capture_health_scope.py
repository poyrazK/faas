from typing import Literal

ObsRuntimePostCaptureHealthScope = Literal["replica_observations_of_shared_fleet"]

OBS_RUNTIME_POST_CAPTURE_HEALTH_SCOPE_VALUES: set[ObsRuntimePostCaptureHealthScope] = {
    "replica_observations_of_shared_fleet",
}


def check_obs_runtime_post_capture_health_scope(value: str) -> ObsRuntimePostCaptureHealthScope:
    if value in OBS_RUNTIME_POST_CAPTURE_HEALTH_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_POST_CAPTURE_HEALTH_SCOPE_VALUES!r}")
