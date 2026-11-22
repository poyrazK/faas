from typing import Literal

AlertDeliveryAnomalyEvidenceComparison = Literal["above_baseline", "below_baseline"]

ALERT_DELIVERY_ANOMALY_EVIDENCE_COMPARISON_VALUES: set[AlertDeliveryAnomalyEvidenceComparison] = {
    "above_baseline",
    "below_baseline",
}


def check_alert_delivery_anomaly_evidence_comparison(value: str) -> AlertDeliveryAnomalyEvidenceComparison:
    if value in ALERT_DELIVERY_ANOMALY_EVIDENCE_COMPARISON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ALERT_DELIVERY_ANOMALY_EVIDENCE_COMPARISON_VALUES!r}"
    )
