from typing import Literal

AlertAnomalyPreviewSkippedWindowReason = Literal[
    "insufficient_history", "insufficient_traffic", "telemetry_missing_or_stale"
]

ALERT_ANOMALY_PREVIEW_SKIPPED_WINDOW_REASON_VALUES: set[AlertAnomalyPreviewSkippedWindowReason] = {
    "insufficient_history",
    "insufficient_traffic",
    "telemetry_missing_or_stale",
}


def check_alert_anomaly_preview_skipped_window_reason(value: str) -> AlertAnomalyPreviewSkippedWindowReason:
    if value in ALERT_ANOMALY_PREVIEW_SKIPPED_WINDOW_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ALERT_ANOMALY_PREVIEW_SKIPPED_WINDOW_REASON_VALUES!r}"
    )
