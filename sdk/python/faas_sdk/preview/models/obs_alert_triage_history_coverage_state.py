from typing import Literal

ObsAlertTriageHistoryCoverageState = Literal["complete", "gaps"]

OBS_ALERT_TRIAGE_HISTORY_COVERAGE_STATE_VALUES: set[ObsAlertTriageHistoryCoverageState] = {
    "complete",
    "gaps",
}


def check_obs_alert_triage_history_coverage_state(value: str) -> ObsAlertTriageHistoryCoverageState:
    if value in OBS_ALERT_TRIAGE_HISTORY_COVERAGE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_TRIAGE_HISTORY_COVERAGE_STATE_VALUES!r}")
