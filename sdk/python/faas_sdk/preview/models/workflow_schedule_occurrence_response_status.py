from typing import Literal

WorkflowScheduleOccurrenceResponseStatus = Literal["skipped_overlap", "skipped_quota", "started"]

WORKFLOW_SCHEDULE_OCCURRENCE_RESPONSE_STATUS_VALUES: set[WorkflowScheduleOccurrenceResponseStatus] = {
    "skipped_overlap",
    "skipped_quota",
    "started",
}


def check_workflow_schedule_occurrence_response_status(value: str) -> WorkflowScheduleOccurrenceResponseStatus:
    if value in WORKFLOW_SCHEDULE_OCCURRENCE_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_SCHEDULE_OCCURRENCE_RESPONSE_STATUS_VALUES!r}"
    )
