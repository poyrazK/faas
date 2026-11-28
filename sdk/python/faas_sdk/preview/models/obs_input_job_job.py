from typing import Literal

ObsInputJobJob = Literal["apid", "gatewayd", "gatewayd-internal", "schedd", "schedd-compute", "vmmd"]

OBS_INPUT_JOB_JOB_VALUES: set[ObsInputJobJob] = {
    "apid",
    "gatewayd",
    "gatewayd-internal",
    "schedd",
    "schedd-compute",
    "vmmd",
}


def check_obs_input_job_job(value: str) -> ObsInputJobJob:
    if value in OBS_INPUT_JOB_JOB_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_INPUT_JOB_JOB_VALUES!r}")
