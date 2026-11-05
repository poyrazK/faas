from typing import Literal

EventRecoveryExecutionFinishedWebhookPayloadUnresolvedCount = Literal[0]

EVENT_RECOVERY_EXECUTION_FINISHED_WEBHOOK_PAYLOAD_UNRESOLVED_COUNT_VALUES: set[
    EventRecoveryExecutionFinishedWebhookPayloadUnresolvedCount
] = {
    0,
}


def check_event_recovery_execution_finished_webhook_payload_unresolved_count(
    value: int,
) -> EventRecoveryExecutionFinishedWebhookPayloadUnresolvedCount:
    if value in EVENT_RECOVERY_EXECUTION_FINISHED_WEBHOOK_PAYLOAD_UNRESOLVED_COUNT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_EXECUTION_FINISHED_WEBHOOK_PAYLOAD_UNRESOLVED_COUNT_VALUES!r}"
    )
