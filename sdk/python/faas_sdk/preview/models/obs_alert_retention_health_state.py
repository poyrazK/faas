from typing import Literal

ObsAlertRetentionHealthState = Literal["current", "failed", "never_run", "stale", "unknown"]

OBS_ALERT_RETENTION_HEALTH_STATE_VALUES: set[ObsAlertRetentionHealthState] = {
    "current",
    "failed",
    "never_run",
    "stale",
    "unknown",
}


def check_obs_alert_retention_health_state(value: str) -> ObsAlertRetentionHealthState:
    if value in OBS_ALERT_RETENTION_HEALTH_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_RETENTION_HEALTH_STATE_VALUES!r}")
