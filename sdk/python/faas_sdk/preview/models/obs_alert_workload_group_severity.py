from typing import Literal

ObsAlertWorkloadGroupSeverity = Literal["critical", "error", "warning"]

OBS_ALERT_WORKLOAD_GROUP_SEVERITY_VALUES: set[ObsAlertWorkloadGroupSeverity] = {
    "critical",
    "error",
    "warning",
}


def check_obs_alert_workload_group_severity(value: str) -> ObsAlertWorkloadGroupSeverity:
    if value in OBS_ALERT_WORKLOAD_GROUP_SEVERITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_WORKLOAD_GROUP_SEVERITY_VALUES!r}")
