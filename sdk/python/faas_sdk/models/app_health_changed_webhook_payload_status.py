from typing import Literal

AppHealthChangedWebhookPayloadStatus = Literal["degraded", "healthy", "unhealthy", "unknown"]

APP_HEALTH_CHANGED_WEBHOOK_PAYLOAD_STATUS_VALUES: set[AppHealthChangedWebhookPayloadStatus] = {
    "degraded",
    "healthy",
    "unhealthy",
    "unknown",
}


def check_app_health_changed_webhook_payload_status(value: str) -> AppHealthChangedWebhookPayloadStatus:
    if value in APP_HEALTH_CHANGED_WEBHOOK_PAYLOAD_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_HEALTH_CHANGED_WEBHOOK_PAYLOAD_STATUS_VALUES!r}")
