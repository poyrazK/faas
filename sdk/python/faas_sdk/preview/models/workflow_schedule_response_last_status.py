from typing import Literal

WorkflowScheduleResponseLastStatus = Literal["armed", "skipped_overlap", "skipped_quota", "started"]

WORKFLOW_SCHEDULE_RESPONSE_LAST_STATUS_VALUES: set[WorkflowScheduleResponseLastStatus] = {
    "armed",
    "skipped_overlap",
    "skipped_quota",
    "started",
}


def check_workflow_schedule_response_last_status(value: str) -> WorkflowScheduleResponseLastStatus:
    if value in WORKFLOW_SCHEDULE_RESPONSE_LAST_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_SCHEDULE_RESPONSE_LAST_STATUS_VALUES!r}")
