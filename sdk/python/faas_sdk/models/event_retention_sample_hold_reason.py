from typing import Literal

EventRetentionSampleHoldReason = Literal["", "backfill_retryable", "backfill_running", "recovery_pending"]

EVENT_RETENTION_SAMPLE_HOLD_REASON_VALUES: set[EventRetentionSampleHoldReason] = {
    "",
    "backfill_retryable",
    "backfill_running",
    "recovery_pending",
}


def check_event_retention_sample_hold_reason(value: str) -> EventRetentionSampleHoldReason:
    if value in EVENT_RETENTION_SAMPLE_HOLD_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RETENTION_SAMPLE_HOLD_REASON_VALUES!r}")
