from typing import Literal

ObsRuntimeCommandCronItemJob = Literal["schedd", "schedd-compute"]

OBS_RUNTIME_COMMAND_CRON_ITEM_JOB_VALUES: set[ObsRuntimeCommandCronItemJob] = {
    "schedd",
    "schedd-compute",
}


def check_obs_runtime_command_cron_item_job(value: str) -> ObsRuntimeCommandCronItemJob:
    if value in OBS_RUNTIME_COMMAND_CRON_ITEM_JOB_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_COMMAND_CRON_ITEM_JOB_VALUES!r}")
