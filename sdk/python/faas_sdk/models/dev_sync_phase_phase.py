from typing import Literal

DevSyncPhasePhase = Literal["boot", "build", "cache", "ready", "route", "sync"]

DEV_SYNC_PHASE_PHASE_VALUES: set[DevSyncPhasePhase] = {
    "boot",
    "build",
    "cache",
    "ready",
    "route",
    "sync",
}


def check_dev_sync_phase_phase(value: str) -> DevSyncPhasePhase:
    if value in DEV_SYNC_PHASE_PHASE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEV_SYNC_PHASE_PHASE_VALUES!r}")
