from typing import Literal

PreviewAlertAnomalyCandidateBaseline = Literal["same_weekday", "zero_baseline_fallback"]

PREVIEW_ALERT_ANOMALY_CANDIDATE_BASELINE_VALUES: set[PreviewAlertAnomalyCandidateBaseline] = {
    "same_weekday",
    "zero_baseline_fallback",
}


def check_preview_alert_anomaly_candidate_baseline(value: str) -> PreviewAlertAnomalyCandidateBaseline:
    if value in PREVIEW_ALERT_ANOMALY_CANDIDATE_BASELINE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PREVIEW_ALERT_ANOMALY_CANDIDATE_BASELINE_VALUES!r}")
