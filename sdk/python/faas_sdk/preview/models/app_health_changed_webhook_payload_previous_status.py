from typing import Literal

AppHealthChangedWebhookPayloadPreviousStatus = Literal["degraded", "healthy", "unhealthy", "unknown"]

APP_HEALTH_CHANGED_WEBHOOK_PAYLOAD_PREVIOUS_STATUS_VALUES: set[AppHealthChangedWebhookPayloadPreviousStatus] = {
    "degraded",
    "healthy",
    "unhealthy",
    "unknown",
}


def check_app_health_changed_webhook_payload_previous_status(
    value: str,
) -> AppHealthChangedWebhookPayloadPreviousStatus:
    if value in APP_HEALTH_CHANGED_WEBHOOK_PAYLOAD_PREVIOUS_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APP_HEALTH_CHANGED_WEBHOOK_PAYLOAD_PREVIOUS_STATUS_VALUES!r}"
    )
