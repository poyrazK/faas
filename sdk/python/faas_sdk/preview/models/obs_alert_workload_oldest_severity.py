from typing import Literal

ObsAlertWorkloadOldestSeverity = Literal["critical", "error", "warning"]

OBS_ALERT_WORKLOAD_OLDEST_SEVERITY_VALUES: set[ObsAlertWorkloadOldestSeverity] = {
    "critical",
    "error",
    "warning",
}


def check_obs_alert_workload_oldest_severity(value: str) -> ObsAlertWorkloadOldestSeverity:
    if value in OBS_ALERT_WORKLOAD_OLDEST_SEVERITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_WORKLOAD_OLDEST_SEVERITY_VALUES!r}")
