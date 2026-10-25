from typing import Literal

ObsAlertWorkloadSummaryAgeBasis = Literal["episode_started_at"]

OBS_ALERT_WORKLOAD_SUMMARY_AGE_BASIS_VALUES: set[ObsAlertWorkloadSummaryAgeBasis] = {
    "episode_started_at",
}


def check_obs_alert_workload_summary_age_basis(value: str) -> ObsAlertWorkloadSummaryAgeBasis:
    if value in OBS_ALERT_WORKLOAD_SUMMARY_AGE_BASIS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_WORKLOAD_SUMMARY_AGE_BASIS_VALUES!r}")
