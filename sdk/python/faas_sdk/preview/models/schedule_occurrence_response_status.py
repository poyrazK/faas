from typing import Literal

ScheduleOccurrenceResponseStatus = Literal[
    "cancelled",
    "coalesced",
    "failed",
    "missed_deadline",
    "pending",
    "queued",
    "running",
    "skipped_overlap",
    "succeeded",
    "uncertain",
    "waiting_replacement",
]

SCHEDULE_OCCURRENCE_RESPONSE_STATUS_VALUES: set[ScheduleOccurrenceResponseStatus] = {
    "cancelled",
    "coalesced",
    "failed",
    "missed_deadline",
    "pending",
    "queued",
    "running",
    "skipped_overlap",
    "succeeded",
    "uncertain",
    "waiting_replacement",
}


def check_schedule_occurrence_response_status(value: str) -> ScheduleOccurrenceResponseStatus:
    if value in SCHEDULE_OCCURRENCE_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCHEDULE_OCCURRENCE_RESPONSE_STATUS_VALUES!r}")
