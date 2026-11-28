from typing import Literal

ObsRuntimeSnapshotPrimeObservationState = Literal["available", "conflicting", "missing", "stale", "unavailable"]

OBS_RUNTIME_SNAPSHOT_PRIME_OBSERVATION_STATE_VALUES: set[ObsRuntimeSnapshotPrimeObservationState] = {
    "available",
    "conflicting",
    "missing",
    "stale",
    "unavailable",
}


def check_obs_runtime_snapshot_prime_observation_state(value: str) -> ObsRuntimeSnapshotPrimeObservationState:
    if value in OBS_RUNTIME_SNAPSHOT_PRIME_OBSERVATION_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_SNAPSHOT_PRIME_OBSERVATION_STATE_VALUES!r}"
    )
