from typing import Literal

RouteHealthTransitionWebhookPayloadHealthStatus = Literal["healthy", "regressed", "unknown"]

ROUTE_HEALTH_TRANSITION_WEBHOOK_PAYLOAD_HEALTH_STATUS_VALUES: set[RouteHealthTransitionWebhookPayloadHealthStatus] = {
    "healthy",
    "regressed",
    "unknown",
}


def check_route_health_transition_webhook_payload_health_status(
    value: str,
) -> RouteHealthTransitionWebhookPayloadHealthStatus:
    if value in ROUTE_HEALTH_TRANSITION_WEBHOOK_PAYLOAD_HEALTH_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_TRANSITION_WEBHOOK_PAYLOAD_HEALTH_STATUS_VALUES!r}"
    )
