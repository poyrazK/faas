from typing import Literal

WorkflowScheduleResponseOverlap = Literal["allow", "skip"]

WORKFLOW_SCHEDULE_RESPONSE_OVERLAP_VALUES: set[WorkflowScheduleResponseOverlap] = {
    "allow",
    "skip",
}


def check_workflow_schedule_response_overlap(value: str) -> WorkflowScheduleResponseOverlap:
    if value in WORKFLOW_SCHEDULE_RESPONSE_OVERLAP_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_SCHEDULE_RESPONSE_OVERLAP_VALUES!r}")
