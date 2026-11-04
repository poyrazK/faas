from typing import Literal

ObsAlertCollectionHistoryIntervalReason = Literal[
    "",
    "collection_failed",
    "no_recent_outcome",
    "persistence_failed",
    "snapshot_rejected",
    "source_read_failed",
    "timeout",
]

OBS_ALERT_COLLECTION_HISTORY_INTERVAL_REASON_VALUES: set[ObsAlertCollectionHistoryIntervalReason] = {
    "",
    "collection_failed",
    "no_recent_outcome",
    "persistence_failed",
    "snapshot_rejected",
    "source_read_failed",
    "timeout",
}


def check_obs_alert_collection_history_interval_reason(value: str) -> ObsAlertCollectionHistoryIntervalReason:
    if value in OBS_ALERT_COLLECTION_HISTORY_INTERVAL_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBS_ALERT_COLLECTION_HISTORY_INTERVAL_REASON_VALUES!r}"
    )
