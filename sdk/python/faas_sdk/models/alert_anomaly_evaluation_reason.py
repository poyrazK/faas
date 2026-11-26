from typing import Literal

AlertAnomalyEvaluationReason = Literal[
    "insufficient_history",
    "insufficient_traffic",
    "minimum_impact_not_reached",
    "notification_cooldown",
    "observed_breach",
    "recovery_confirmation_pending",
    "recovery_confirmed",
    "telemetry_missing_or_stale",
    "telemetry_unavailable",
    "threshold_not_reached",
    "zero_baseline",
]

ALERT_ANOMALY_EVALUATION_REASON_VALUES: set[AlertAnomalyEvaluationReason] = {
    "insufficient_history",
    "insufficient_traffic",
    "minimum_impact_not_reached",
    "notification_cooldown",
    "observed_breach",
    "recovery_confirmation_pending",
    "recovery_confirmed",
    "telemetry_missing_or_stale",
    "telemetry_unavailable",
    "threshold_not_reached",
    "zero_baseline",
}


def check_alert_anomaly_evaluation_reason(value: str) -> AlertAnomalyEvaluationReason:
    if value in ALERT_ANOMALY_EVALUATION_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ALERT_ANOMALY_EVALUATION_REASON_VALUES!r}")
