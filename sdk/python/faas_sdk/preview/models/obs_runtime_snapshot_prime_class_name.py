from typing import Literal

ObsRuntimeSnapshotPrimeClassName = Literal[
    "active", "publication_pending", "release_blocked", "snapshot_recorded", "unknown", "waiting"
]

OBS_RUNTIME_SNAPSHOT_PRIME_CLASS_NAME_VALUES: set[ObsRuntimeSnapshotPrimeClassName] = {
    "active",
    "publication_pending",
    "release_blocked",
    "snapshot_recorded",
    "unknown",
    "waiting",
}


def check_obs_runtime_snapshot_prime_class_name(value: str) -> ObsRuntimeSnapshotPrimeClassName:
    if value in OBS_RUNTIME_SNAPSHOT_PRIME_CLASS_NAME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_SNAPSHOT_PRIME_CLASS_NAME_VALUES!r}")
