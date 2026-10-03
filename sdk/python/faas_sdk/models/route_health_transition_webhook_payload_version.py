from typing import Literal

RouteHealthTransitionWebhookPayloadVersion = Literal[1]

ROUTE_HEALTH_TRANSITION_WEBHOOK_PAYLOAD_VERSION_VALUES: set[RouteHealthTransitionWebhookPayloadVersion] = {
    1,
}


def check_route_health_transition_webhook_payload_version(value: int) -> RouteHealthTransitionWebhookPayloadVersion:
    if value in ROUTE_HEALTH_TRANSITION_WEBHOOK_PAYLOAD_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_TRANSITION_WEBHOOK_PAYLOAD_VERSION_VALUES!r}"
    )
