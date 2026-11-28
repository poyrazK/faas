from typing import Literal

ObsRuntimeSnapshotPrimeClassState = Literal["conflicting", "empty", "missing", "nonempty", "stale", "unavailable"]

OBS_RUNTIME_SNAPSHOT_PRIME_CLASS_STATE_VALUES: set[ObsRuntimeSnapshotPrimeClassState] = {
    "conflicting",
    "empty",
    "missing",
    "nonempty",
    "stale",
    "unavailable",
}


def check_obs_runtime_snapshot_prime_class_state(value: str) -> ObsRuntimeSnapshotPrimeClassState:
    if value in OBS_RUNTIME_SNAPSHOT_PRIME_CLASS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_SNAPSHOT_PRIME_CLASS_STATE_VALUES!r}")
