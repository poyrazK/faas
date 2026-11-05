from typing import Literal

EventRecoveryFinishedWebhookPayloadPendingCount = Literal[0]

EVENT_RECOVERY_FINISHED_WEBHOOK_PAYLOAD_PENDING_COUNT_VALUES: set[EventRecoveryFinishedWebhookPayloadPendingCount] = {
    0,
}


def check_event_recovery_finished_webhook_payload_pending_count(
    value: int,
) -> EventRecoveryFinishedWebhookPayloadPendingCount:
    if value in EVENT_RECOVERY_FINISHED_WEBHOOK_PAYLOAD_PENDING_COUNT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_FINISHED_WEBHOOK_PAYLOAD_PENDING_COUNT_VALUES!r}"
    )
