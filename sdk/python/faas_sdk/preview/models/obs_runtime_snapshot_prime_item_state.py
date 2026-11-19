from typing import Literal

ObsRuntimeSnapshotPrimeItemState = Literal["available", "conflicting", "missing", "stale", "unavailable"]

OBS_RUNTIME_SNAPSHOT_PRIME_ITEM_STATE_VALUES: set[ObsRuntimeSnapshotPrimeItemState] = {
    "available",
    "conflicting",
    "missing",
    "stale",
    "unavailable",
}


def check_obs_runtime_snapshot_prime_item_state(value: str) -> ObsRuntimeSnapshotPrimeItemState:
    if value in OBS_RUNTIME_SNAPSHOT_PRIME_ITEM_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_SNAPSHOT_PRIME_ITEM_STATE_VALUES!r}")
