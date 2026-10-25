from typing import Literal

AppHealthChangedWebhookPayloadChange = Literal["confirmed", "improved", "unconfirmed", "worsened"]

APP_HEALTH_CHANGED_WEBHOOK_PAYLOAD_CHANGE_VALUES: set[AppHealthChangedWebhookPayloadChange] = {
    "confirmed",
    "improved",
    "unconfirmed",
    "worsened",
}


def check_app_health_changed_webhook_payload_change(value: str) -> AppHealthChangedWebhookPayloadChange:
    if value in APP_HEALTH_CHANGED_WEBHOOK_PAYLOAD_CHANGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_HEALTH_CHANGED_WEBHOOK_PAYLOAD_CHANGE_VALUES!r}")
