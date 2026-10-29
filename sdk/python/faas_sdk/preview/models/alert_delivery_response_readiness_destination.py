from typing import Literal

AlertDeliveryResponseReadinessDestination = Literal["fallback", "primary"]

ALERT_DELIVERY_RESPONSE_READINESS_DESTINATION_VALUES: set[AlertDeliveryResponseReadinessDestination] = {
    "fallback",
    "primary",
}


def check_alert_delivery_response_readiness_destination(value: str) -> AlertDeliveryResponseReadinessDestination:
    if value in ALERT_DELIVERY_RESPONSE_READINESS_DESTINATION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ALERT_DELIVERY_RESPONSE_READINESS_DESTINATION_VALUES!r}"
    )
