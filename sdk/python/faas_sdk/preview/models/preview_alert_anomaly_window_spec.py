from typing import Literal

PreviewAlertAnomalyWindowSpec = Literal["15d", "15m", "1h", "24h", "5m", "6h", "7d"]

PREVIEW_ALERT_ANOMALY_WINDOW_SPEC_VALUES: set[PreviewAlertAnomalyWindowSpec] = {
    "15d",
    "15m",
    "1h",
    "24h",
    "5m",
    "6h",
    "7d",
}


def check_preview_alert_anomaly_window_spec(value: str) -> PreviewAlertAnomalyWindowSpec:
    if value in PREVIEW_ALERT_ANOMALY_WINDOW_SPEC_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PREVIEW_ALERT_ANOMALY_WINDOW_SPEC_VALUES!r}")
