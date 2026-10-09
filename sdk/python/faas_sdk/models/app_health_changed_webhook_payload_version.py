from typing import Literal

AppHealthChangedWebhookPayloadVersion = Literal[1]

APP_HEALTH_CHANGED_WEBHOOK_PAYLOAD_VERSION_VALUES: set[AppHealthChangedWebhookPayloadVersion] = {
    1,
}


def check_app_health_changed_webhook_payload_version(value: int) -> AppHealthChangedWebhookPayloadVersion:
    if value in APP_HEALTH_CHANGED_WEBHOOK_PAYLOAD_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APP_HEALTH_CHANGED_WEBHOOK_PAYLOAD_VERSION_VALUES!r}"
    )
