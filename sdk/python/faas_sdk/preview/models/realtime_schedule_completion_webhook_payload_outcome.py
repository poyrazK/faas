from typing import Literal

RealtimeScheduleCompletionWebhookPayloadOutcome = Literal["failed", "published", "skipped"]

REALTIME_SCHEDULE_COMPLETION_WEBHOOK_PAYLOAD_OUTCOME_VALUES: set[RealtimeScheduleCompletionWebhookPayloadOutcome] = {
    "failed",
    "published",
    "skipped",
}


def check_realtime_schedule_completion_webhook_payload_outcome(
    value: str,
) -> RealtimeScheduleCompletionWebhookPayloadOutcome:
    if value in REALTIME_SCHEDULE_COMPLETION_WEBHOOK_PAYLOAD_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REALTIME_SCHEDULE_COMPLETION_WEBHOOK_PAYLOAD_OUTCOME_VALUES!r}"
    )
