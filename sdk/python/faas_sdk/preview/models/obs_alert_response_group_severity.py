from typing import Literal

ObsAlertResponseGroupSeverity = Literal["critical", "error", "warning"]

OBS_ALERT_RESPONSE_GROUP_SEVERITY_VALUES: set[ObsAlertResponseGroupSeverity] = {
    "critical",
    "error",
    "warning",
}


def check_obs_alert_response_group_severity(value: str) -> ObsAlertResponseGroupSeverity:
    if value in OBS_ALERT_RESPONSE_GROUP_SEVERITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_RESPONSE_GROUP_SEVERITY_VALUES!r}")
