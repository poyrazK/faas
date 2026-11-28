from typing import Literal

ObsRuntimeBuildRecoveryItemJob = Literal["apid"]

OBS_RUNTIME_BUILD_RECOVERY_ITEM_JOB_VALUES: set[ObsRuntimeBuildRecoveryItemJob] = {
    "apid",
}


def check_obs_runtime_build_recovery_item_job(value: str) -> ObsRuntimeBuildRecoveryItemJob:
    if value in OBS_RUNTIME_BUILD_RECOVERY_ITEM_JOB_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_BUILD_RECOVERY_ITEM_JOB_VALUES!r}")
