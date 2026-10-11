from typing import Literal

AppHealthChangedWebhookPayloadScope = Literal["default"]

APP_HEALTH_CHANGED_WEBHOOK_PAYLOAD_SCOPE_VALUES: set[AppHealthChangedWebhookPayloadScope] = {
    "default",
}


def check_app_health_changed_webhook_payload_scope(value: str) -> AppHealthChangedWebhookPayloadScope:
    if value in APP_HEALTH_CHANGED_WEBHOOK_PAYLOAD_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_HEALTH_CHANGED_WEBHOOK_PAYLOAD_SCOPE_VALUES!r}")
