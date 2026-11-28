from typing import Literal

EventRecoveryFinishedWebhookPayloadOutcome = Literal["cancelled", "completed", "expired"]

EVENT_RECOVERY_FINISHED_WEBHOOK_PAYLOAD_OUTCOME_VALUES: set[EventRecoveryFinishedWebhookPayloadOutcome] = {
    "cancelled",
    "completed",
    "expired",
}


def check_event_recovery_finished_webhook_payload_outcome(value: str) -> EventRecoveryFinishedWebhookPayloadOutcome:
    if value in EVENT_RECOVERY_FINISHED_WEBHOOK_PAYLOAD_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_FINISHED_WEBHOOK_PAYLOAD_OUTCOME_VALUES!r}"
    )
