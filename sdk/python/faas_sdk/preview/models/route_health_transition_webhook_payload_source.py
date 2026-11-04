from typing import Literal

RouteHealthTransitionWebhookPayloadSource = Literal["manual", "worker"]

ROUTE_HEALTH_TRANSITION_WEBHOOK_PAYLOAD_SOURCE_VALUES: set[RouteHealthTransitionWebhookPayloadSource] = {
    "manual",
    "worker",
}


def check_route_health_transition_webhook_payload_source(value: str) -> RouteHealthTransitionWebhookPayloadSource:
    if value in ROUTE_HEALTH_TRANSITION_WEBHOOK_PAYLOAD_SOURCE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_TRANSITION_WEBHOOK_PAYLOAD_SOURCE_VALUES!r}"
    )
