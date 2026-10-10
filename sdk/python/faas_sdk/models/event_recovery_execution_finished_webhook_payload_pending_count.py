from typing import Literal

EventRecoveryExecutionFinishedWebhookPayloadPendingCount = Literal[0]

EVENT_RECOVERY_EXECUTION_FINISHED_WEBHOOK_PAYLOAD_PENDING_COUNT_VALUES: set[
    EventRecoveryExecutionFinishedWebhookPayloadPendingCount
] = {
    0,
}


def check_event_recovery_execution_finished_webhook_payload_pending_count(
    value: int,
) -> EventRecoveryExecutionFinishedWebhookPayloadPendingCount:
    if value in EVENT_RECOVERY_EXECUTION_FINISHED_WEBHOOK_PAYLOAD_PENDING_COUNT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_EXECUTION_FINISHED_WEBHOOK_PAYLOAD_PENDING_COUNT_VALUES!r}"
    )
