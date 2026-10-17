from typing import Literal

ObsAlertTriageHistoryCoverageReason = Literal["missing_receipts"]

OBS_ALERT_TRIAGE_HISTORY_COVERAGE_REASON_VALUES: set[ObsAlertTriageHistoryCoverageReason] = {
    "missing_receipts",
}


def check_obs_alert_triage_history_coverage_reason(value: str) -> ObsAlertTriageHistoryCoverageReason:
    if value in OBS_ALERT_TRIAGE_HISTORY_COVERAGE_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_TRIAGE_HISTORY_COVERAGE_REASON_VALUES!r}")
