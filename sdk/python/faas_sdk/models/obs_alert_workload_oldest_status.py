from typing import Literal

ObsAlertWorkloadOldestStatus = Literal["firing", "unknown"]

OBS_ALERT_WORKLOAD_OLDEST_STATUS_VALUES: set[ObsAlertWorkloadOldestStatus] = {
    "firing",
    "unknown",
}


def check_obs_alert_workload_oldest_status(value: str) -> ObsAlertWorkloadOldestStatus:
    if value in OBS_ALERT_WORKLOAD_OLDEST_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_WORKLOAD_OLDEST_STATUS_VALUES!r}")
