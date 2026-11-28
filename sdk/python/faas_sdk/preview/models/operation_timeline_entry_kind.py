from typing import Literal

OperationTimelineEntryKind = Literal["attempt", "delivery_attempt", "event", "step_attempt"]

OPERATION_TIMELINE_ENTRY_KIND_VALUES: set[OperationTimelineEntryKind] = {
    "attempt",
    "delivery_attempt",
    "event",
    "step_attempt",
}


def check_operation_timeline_entry_kind(value: str) -> OperationTimelineEntryKind:
    if value in OPERATION_TIMELINE_ENTRY_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_TIMELINE_ENTRY_KIND_VALUES!r}")
