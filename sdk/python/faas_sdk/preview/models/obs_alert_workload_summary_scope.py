from typing import Literal

ObsAlertWorkloadSummaryScope = Literal["retained_platform_alert_episodes"]

OBS_ALERT_WORKLOAD_SUMMARY_SCOPE_VALUES: set[ObsAlertWorkloadSummaryScope] = {
    "retained_platform_alert_episodes",
}


def check_obs_alert_workload_summary_scope(value: str) -> ObsAlertWorkloadSummaryScope:
    if value in OBS_ALERT_WORKLOAD_SUMMARY_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_WORKLOAD_SUMMARY_SCOPE_VALUES!r}")
