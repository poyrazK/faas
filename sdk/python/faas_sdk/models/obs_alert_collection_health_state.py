from typing import Literal

ObsAlertCollectionHealthState = Literal["current", "unknown"]

OBS_ALERT_COLLECTION_HEALTH_STATE_VALUES: set[ObsAlertCollectionHealthState] = {
    "current",
    "unknown",
}


def check_obs_alert_collection_health_state(value: str) -> ObsAlertCollectionHealthState:
    if value in OBS_ALERT_COLLECTION_HEALTH_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_COLLECTION_HEALTH_STATE_VALUES!r}")
