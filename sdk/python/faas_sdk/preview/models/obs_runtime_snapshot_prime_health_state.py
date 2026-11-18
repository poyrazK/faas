from typing import Literal

ObsRuntimeSnapshotPrimeHealthState = Literal["empty", "incomplete", "nonempty", "unknown"]

OBS_RUNTIME_SNAPSHOT_PRIME_HEALTH_STATE_VALUES: set[ObsRuntimeSnapshotPrimeHealthState] = {
    "empty",
    "incomplete",
    "nonempty",
    "unknown",
}


def check_obs_runtime_snapshot_prime_health_state(value: str) -> ObsRuntimeSnapshotPrimeHealthState:
    if value in OBS_RUNTIME_SNAPSHOT_PRIME_HEALTH_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_SNAPSHOT_PRIME_HEALTH_STATE_VALUES!r}")
