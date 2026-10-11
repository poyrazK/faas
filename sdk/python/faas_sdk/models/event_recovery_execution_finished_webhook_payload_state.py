from typing import Literal

EventRecoveryExecutionFinishedWebhookPayloadState = Literal["cancelled", "completed"]

EVENT_RECOVERY_EXECUTION_FINISHED_WEBHOOK_PAYLOAD_STATE_VALUES: set[
    EventRecoveryExecutionFinishedWebhookPayloadState
] = {
    "cancelled",
    "completed",
}


def check_event_recovery_execution_finished_webhook_payload_state(
    value: str,
) -> EventRecoveryExecutionFinishedWebhookPayloadState:
    if value in EVENT_RECOVERY_EXECUTION_FINISHED_WEBHOOK_PAYLOAD_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_EXECUTION_FINISHED_WEBHOOK_PAYLOAD_STATE_VALUES!r}"
    )
