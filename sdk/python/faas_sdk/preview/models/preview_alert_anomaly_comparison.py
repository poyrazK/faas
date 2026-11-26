from typing import Literal

PreviewAlertAnomalyComparison = Literal["above_baseline", "below_baseline"]

PREVIEW_ALERT_ANOMALY_COMPARISON_VALUES: set[PreviewAlertAnomalyComparison] = {
    "above_baseline",
    "below_baseline",
}


def check_preview_alert_anomaly_comparison(value: str) -> PreviewAlertAnomalyComparison:
    if value in PREVIEW_ALERT_ANOMALY_COMPARISON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PREVIEW_ALERT_ANOMALY_COMPARISON_VALUES!r}")
