from typing import Literal

EventRecoveryExecutionFinishedWebhookPayloadMode = Literal["execution"]

EVENT_RECOVERY_EXECUTION_FINISHED_WEBHOOK_PAYLOAD_MODE_VALUES: set[EventRecoveryExecutionFinishedWebhookPayloadMode] = {
    "execution",
}


def check_event_recovery_execution_finished_webhook_payload_mode(
    value: str,
) -> EventRecoveryExecutionFinishedWebhookPayloadMode:
    if value in EVENT_RECOVERY_EXECUTION_FINISHED_WEBHOOK_PAYLOAD_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_EXECUTION_FINISHED_WEBHOOK_PAYLOAD_MODE_VALUES!r}"
    )
