from typing import Literal

ObsInputJobState = Literal["degraded", "healthy", "partial", "unknown"]

OBS_INPUT_JOB_STATE_VALUES: set[ObsInputJobState] = {
    "degraded",
    "healthy",
    "partial",
    "unknown",
}


def check_obs_input_job_state(value: str) -> ObsInputJobState:
    if value in OBS_INPUT_JOB_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_INPUT_JOB_STATE_VALUES!r}")
