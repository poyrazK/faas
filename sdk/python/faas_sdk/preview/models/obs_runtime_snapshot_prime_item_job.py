from typing import Literal

ObsRuntimeSnapshotPrimeItemJob = Literal["schedd", "schedd-compute"]

OBS_RUNTIME_SNAPSHOT_PRIME_ITEM_JOB_VALUES: set[ObsRuntimeSnapshotPrimeItemJob] = {
    "schedd",
    "schedd-compute",
}


def check_obs_runtime_snapshot_prime_item_job(value: str) -> ObsRuntimeSnapshotPrimeItemJob:
    if value in OBS_RUNTIME_SNAPSHOT_PRIME_ITEM_JOB_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_SNAPSHOT_PRIME_ITEM_JOB_VALUES!r}")
