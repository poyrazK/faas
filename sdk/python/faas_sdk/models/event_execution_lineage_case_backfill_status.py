from typing import Literal

EventExecutionLineageCaseBackfillStatus = Literal[
    "cancelled", "completed", "expired", "not_requested", "paused", "pending", "waiting_for_retry_jobs"
]

EVENT_EXECUTION_LINEAGE_CASE_BACKFILL_STATUS_VALUES: set[EventExecutionLineageCaseBackfillStatus] = {
    "cancelled",
    "completed",
    "expired",
    "not_requested",
    "paused",
    "pending",
    "waiting_for_retry_jobs",
}


def check_event_execution_lineage_case_backfill_status(value: str) -> EventExecutionLineageCaseBackfillStatus:
    if value in EVENT_EXECUTION_LINEAGE_CASE_BACKFILL_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_EXECUTION_LINEAGE_CASE_BACKFILL_STATUS_VALUES!r}"
    )
