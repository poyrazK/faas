from typing import Literal

ObsRuntimeBuildJobState = Literal["available", "unknown"]

OBS_RUNTIME_BUILD_JOB_STATE_VALUES: set[ObsRuntimeBuildJobState] = {
    "available",
    "unknown",
}


def check_obs_runtime_build_job_state(value: str) -> ObsRuntimeBuildJobState:
    if value in OBS_RUNTIME_BUILD_JOB_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_BUILD_JOB_STATE_VALUES!r}")
