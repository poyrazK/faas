from typing import Literal

DevSyncPhaseStatus = Literal["completed", "failed", "in_progress"]

DEV_SYNC_PHASE_STATUS_VALUES: set[DevSyncPhaseStatus] = {
    "completed",
    "failed",
    "in_progress",
}


def check_dev_sync_phase_status(value: str) -> DevSyncPhaseStatus:
    if value in DEV_SYNC_PHASE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEV_SYNC_PHASE_STATUS_VALUES!r}")
