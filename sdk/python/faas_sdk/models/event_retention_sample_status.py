from typing import Literal

EventRetentionSampleStatus = Literal["eligible_for_pruning", "expiring", "held"]

EVENT_RETENTION_SAMPLE_STATUS_VALUES: set[EventRetentionSampleStatus] = {
    "eligible_for_pruning",
    "expiring",
    "held",
}


def check_event_retention_sample_status(value: str) -> EventRetentionSampleStatus:
    if value in EVENT_RETENTION_SAMPLE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RETENTION_SAMPLE_STATUS_VALUES!r}")
