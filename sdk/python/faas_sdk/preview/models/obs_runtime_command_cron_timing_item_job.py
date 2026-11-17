from typing import Literal

ObsRuntimeCommandCronTimingItemJob = Literal["schedd", "schedd-compute"]

OBS_RUNTIME_COMMAND_CRON_TIMING_ITEM_JOB_VALUES: set[ObsRuntimeCommandCronTimingItemJob] = {
    "schedd",
    "schedd-compute",
}


def check_obs_runtime_command_cron_timing_item_job(value: str) -> ObsRuntimeCommandCronTimingItemJob:
    if value in OBS_RUNTIME_COMMAND_CRON_TIMING_ITEM_JOB_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_COMMAND_CRON_TIMING_ITEM_JOB_VALUES!r}")
