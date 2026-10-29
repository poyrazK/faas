from typing import Literal

ObsAlertWorkloadGroupStatus = Literal["firing", "resolved", "unknown"]

OBS_ALERT_WORKLOAD_GROUP_STATUS_VALUES: set[ObsAlertWorkloadGroupStatus] = {
    "firing",
    "resolved",
    "unknown",
}


def check_obs_alert_workload_group_status(value: str) -> ObsAlertWorkloadGroupStatus:
    if value in OBS_ALERT_WORKLOAD_GROUP_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_WORKLOAD_GROUP_STATUS_VALUES!r}")
