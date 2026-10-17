from typing import Literal

ObsAlertSuppressionState = Literal[
    "active", "disabled", "not_applicable", "stale", "suppressed", "unavailable", "unknown", "unmatched", "unprocessed"
]

OBS_ALERT_SUPPRESSION_STATE_VALUES: set[ObsAlertSuppressionState] = {
    "active",
    "disabled",
    "not_applicable",
    "stale",
    "suppressed",
    "unavailable",
    "unknown",
    "unmatched",
    "unprocessed",
}


def check_obs_alert_suppression_state(value: str) -> ObsAlertSuppressionState:
    if value in OBS_ALERT_SUPPRESSION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_SUPPRESSION_STATE_VALUES!r}")
