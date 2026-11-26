from typing import Literal

AlertDeliveryAnomalyEvidenceMetric = Literal[
    "cold_start_pct", "error_rate_pct", "latency_p95_ms", "latency_p99_ms", "request_count"
]

ALERT_DELIVERY_ANOMALY_EVIDENCE_METRIC_VALUES: set[AlertDeliveryAnomalyEvidenceMetric] = {
    "cold_start_pct",
    "error_rate_pct",
    "latency_p95_ms",
    "latency_p99_ms",
    "request_count",
}


def check_alert_delivery_anomaly_evidence_metric(value: str) -> AlertDeliveryAnomalyEvidenceMetric:
    if value in ALERT_DELIVERY_ANOMALY_EVIDENCE_METRIC_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ALERT_DELIVERY_ANOMALY_EVIDENCE_METRIC_VALUES!r}")
