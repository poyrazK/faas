from typing import Literal

ObsAlertTriageRequestAction = Literal["acknowledge", "claim", "release", "snooze", "unacknowledge", "unsnooze"]

OBS_ALERT_TRIAGE_REQUEST_ACTION_VALUES: set[ObsAlertTriageRequestAction] = {
    "acknowledge",
    "claim",
    "release",
    "snooze",
    "unacknowledge",
    "unsnooze",
}


def check_obs_alert_triage_request_action(value: str) -> ObsAlertTriageRequestAction:
    if value in OBS_ALERT_TRIAGE_REQUEST_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_TRIAGE_REQUEST_ACTION_VALUES!r}")
