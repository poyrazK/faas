from typing import Literal

ObsAlertCollectionHealthCollectionState = Literal[
    "current", "disabled", "failed", "never_collected", "stale", "unknown"
]

OBS_ALERT_COLLECTION_HEALTH_COLLECTION_STATE_VALUES: set[ObsAlertCollectionHealthCollectionState] = {
    "current",
    "disabled",
    "failed",
    "never_collected",
    "stale",
    "unknown",
}


def check_obs_alert_collection_health_collection_state(value: str) -> ObsAlertCollectionHealthCollectionState:
    if value in OBS_ALERT_COLLECTION_HEALTH_COLLECTION_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBS_ALERT_COLLECTION_HEALTH_COLLECTION_STATE_VALUES!r}"
    )
