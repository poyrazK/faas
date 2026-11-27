from typing import Literal

PreviewAlertAnomalyMetric = Literal["error_rate_pct", "latency_p95_ms", "request_count"]

PREVIEW_ALERT_ANOMALY_METRIC_VALUES: set[PreviewAlertAnomalyMetric] = {
    "error_rate_pct",
    "latency_p95_ms",
    "request_count",
}


def check_preview_alert_anomaly_metric(value: str) -> PreviewAlertAnomalyMetric:
    if value in PREVIEW_ALERT_ANOMALY_METRIC_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PREVIEW_ALERT_ANOMALY_METRIC_VALUES!r}")
