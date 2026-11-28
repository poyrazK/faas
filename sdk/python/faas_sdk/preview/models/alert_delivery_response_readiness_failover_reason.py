from typing import Literal

AlertDeliveryResponseReadinessFailoverReason = Literal[
    "incident_sticky_fallback", "primary_failure_threshold", "primary_queue_blocked", "recovery_to_primary"
]

ALERT_DELIVERY_RESPONSE_READINESS_FAILOVER_REASON_VALUES: set[AlertDeliveryResponseReadinessFailoverReason] = {
    "incident_sticky_fallback",
    "primary_failure_threshold",
    "primary_queue_blocked",
    "recovery_to_primary",
}


def check_alert_delivery_response_readiness_failover_reason(value: str) -> AlertDeliveryResponseReadinessFailoverReason:
    if value in ALERT_DELIVERY_RESPONSE_READINESS_FAILOVER_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ALERT_DELIVERY_RESPONSE_READINESS_FAILOVER_REASON_VALUES!r}"
    )
