from typing import Literal

EventRecoveryExecutionFinishedWebhookPayloadOutcome = Literal["all_succeeded", "finished_with_non_success"]

EVENT_RECOVERY_EXECUTION_FINISHED_WEBHOOK_PAYLOAD_OUTCOME_VALUES: set[
    EventRecoveryExecutionFinishedWebhookPayloadOutcome
] = {
    "all_succeeded",
    "finished_with_non_success",
}


def check_event_recovery_execution_finished_webhook_payload_outcome(
    value: str,
) -> EventRecoveryExecutionFinishedWebhookPayloadOutcome:
    if value in EVENT_RECOVERY_EXECUTION_FINISHED_WEBHOOK_PAYLOAD_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_EXECUTION_FINISHED_WEBHOOK_PAYLOAD_OUTCOME_VALUES!r}"
    )
