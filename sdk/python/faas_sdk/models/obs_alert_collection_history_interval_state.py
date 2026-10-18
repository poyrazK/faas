from typing import Literal

ObsAlertCollectionHistoryIntervalState = Literal["failure", "success", "unknown"]

OBS_ALERT_COLLECTION_HISTORY_INTERVAL_STATE_VALUES: set[ObsAlertCollectionHistoryIntervalState] = {
    "failure",
    "success",
    "unknown",
}


def check_obs_alert_collection_history_interval_state(value: str) -> ObsAlertCollectionHistoryIntervalState:
    if value in OBS_ALERT_COLLECTION_HISTORY_INTERVAL_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBS_ALERT_COLLECTION_HISTORY_INTERVAL_STATE_VALUES!r}"
    )
