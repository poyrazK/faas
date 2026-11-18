from typing import Literal

ObsRuntimeCommandCronOutcomeItemJob = Literal["schedd", "schedd-compute"]

OBS_RUNTIME_COMMAND_CRON_OUTCOME_ITEM_JOB_VALUES: set[ObsRuntimeCommandCronOutcomeItemJob] = {
    "schedd",
    "schedd-compute",
}


def check_obs_runtime_command_cron_outcome_item_job(value: str) -> ObsRuntimeCommandCronOutcomeItemJob:
    if value in OBS_RUNTIME_COMMAND_CRON_OUTCOME_ITEM_JOB_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_COMMAND_CRON_OUTCOME_ITEM_JOB_VALUES!r}")
