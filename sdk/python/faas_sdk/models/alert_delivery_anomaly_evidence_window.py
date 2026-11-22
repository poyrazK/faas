from typing import Literal

AlertDeliveryAnomalyEvidenceWindow = Literal["15d", "15m", "1h", "24h", "5m", "6h", "7d"]

ALERT_DELIVERY_ANOMALY_EVIDENCE_WINDOW_VALUES: set[AlertDeliveryAnomalyEvidenceWindow] = {
    "15d",
    "15m",
    "1h",
    "24h",
    "5m",
    "6h",
    "7d",
}


def check_alert_delivery_anomaly_evidence_window(value: str) -> AlertDeliveryAnomalyEvidenceWindow:
    if value in ALERT_DELIVERY_ANOMALY_EVIDENCE_WINDOW_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ALERT_DELIVERY_ANOMALY_EVIDENCE_WINDOW_VALUES!r}")
