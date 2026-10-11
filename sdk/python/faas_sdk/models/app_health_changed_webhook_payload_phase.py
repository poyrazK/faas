from typing import Literal

AppHealthChangedWebhookPayloadPhase = Literal[
    "deploying", "idle", "maintenance", "not_deployed", "serving", "unknown", "unsupported"
]

APP_HEALTH_CHANGED_WEBHOOK_PAYLOAD_PHASE_VALUES: set[AppHealthChangedWebhookPayloadPhase] = {
    "deploying",
    "idle",
    "maintenance",
    "not_deployed",
    "serving",
    "unknown",
    "unsupported",
}


def check_app_health_changed_webhook_payload_phase(value: str) -> AppHealthChangedWebhookPayloadPhase:
    if value in APP_HEALTH_CHANGED_WEBHOOK_PAYLOAD_PHASE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_HEALTH_CHANGED_WEBHOOK_PAYLOAD_PHASE_VALUES!r}")
