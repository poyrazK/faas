from typing import Literal

EventExecutionLineageCaseStatus = Literal["already_associated", "conflict", "repaired", "unresolved"]

EVENT_EXECUTION_LINEAGE_CASE_STATUS_VALUES: set[EventExecutionLineageCaseStatus] = {
    "already_associated",
    "conflict",
    "repaired",
    "unresolved",
}


def check_event_execution_lineage_case_status(value: str) -> EventExecutionLineageCaseStatus:
    if value in EVENT_EXECUTION_LINEAGE_CASE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_EXECUTION_LINEAGE_CASE_STATUS_VALUES!r}")
