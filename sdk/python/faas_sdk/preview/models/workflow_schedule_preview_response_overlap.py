from typing import Literal

WorkflowSchedulePreviewResponseOverlap = Literal["allow", "skip"]

WORKFLOW_SCHEDULE_PREVIEW_RESPONSE_OVERLAP_VALUES: set[WorkflowSchedulePreviewResponseOverlap] = {
    "allow",
    "skip",
}


def check_workflow_schedule_preview_response_overlap(value: str) -> WorkflowSchedulePreviewResponseOverlap:
    if value in WORKFLOW_SCHEDULE_PREVIEW_RESPONSE_OVERLAP_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_SCHEDULE_PREVIEW_RESPONSE_OVERLAP_VALUES!r}"
    )
