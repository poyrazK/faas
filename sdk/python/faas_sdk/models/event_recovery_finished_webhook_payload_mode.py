from typing import Literal

EventRecoveryFinishedWebhookPayloadMode = Literal["execution", "routing"]

EVENT_RECOVERY_FINISHED_WEBHOOK_PAYLOAD_MODE_VALUES: set[EventRecoveryFinishedWebhookPayloadMode] = {
    "execution",
    "routing",
}


def check_event_recovery_finished_webhook_payload_mode(value: str) -> EventRecoveryFinishedWebhookPayloadMode:
    if value in EVENT_RECOVERY_FINISHED_WEBHOOK_PAYLOAD_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_FINISHED_WEBHOOK_PAYLOAD_MODE_VALUES!r}"
    )
