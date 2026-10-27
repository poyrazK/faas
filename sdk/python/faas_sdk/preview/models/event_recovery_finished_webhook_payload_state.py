from typing import Literal

EventRecoveryFinishedWebhookPayloadState = Literal["cancelled", "completed"]

EVENT_RECOVERY_FINISHED_WEBHOOK_PAYLOAD_STATE_VALUES: set[EventRecoveryFinishedWebhookPayloadState] = {
    "cancelled",
    "completed",
}


def check_event_recovery_finished_webhook_payload_state(value: str) -> EventRecoveryFinishedWebhookPayloadState:
    if value in EVENT_RECOVERY_FINISHED_WEBHOOK_PAYLOAD_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_FINISHED_WEBHOOK_PAYLOAD_STATE_VALUES!r}"
    )
