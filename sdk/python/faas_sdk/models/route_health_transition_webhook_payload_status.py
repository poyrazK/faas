from typing import Literal

RouteHealthTransitionWebhookPayloadStatus = Literal["aborted", "blocked", "resumed"]

ROUTE_HEALTH_TRANSITION_WEBHOOK_PAYLOAD_STATUS_VALUES: set[RouteHealthTransitionWebhookPayloadStatus] = {
    "aborted",
    "blocked",
    "resumed",
}


def check_route_health_transition_webhook_payload_status(value: str) -> RouteHealthTransitionWebhookPayloadStatus:
    if value in ROUTE_HEALTH_TRANSITION_WEBHOOK_PAYLOAD_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_TRANSITION_WEBHOOK_PAYLOAD_STATUS_VALUES!r}"
    )
