from typing import Literal

ObsAlertCollectionFailureReason = Literal[
    "collection_failed", "persistence_failed", "snapshot_rejected", "source_read_failed", "timeout"
]

OBS_ALERT_COLLECTION_FAILURE_REASON_VALUES: set[ObsAlertCollectionFailureReason] = {
    "collection_failed",
    "persistence_failed",
    "snapshot_rejected",
    "source_read_failed",
    "timeout",
}


def check_obs_alert_collection_failure_reason(value: str) -> ObsAlertCollectionFailureReason:
    if value in OBS_ALERT_COLLECTION_FAILURE_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_COLLECTION_FAILURE_REASON_VALUES!r}")
