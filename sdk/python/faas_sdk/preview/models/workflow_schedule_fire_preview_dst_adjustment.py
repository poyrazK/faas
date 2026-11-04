from typing import Literal

WorkflowScheduleFirePreviewDstAdjustment = Literal["shifted_forward"]

WORKFLOW_SCHEDULE_FIRE_PREVIEW_DST_ADJUSTMENT_VALUES: set[WorkflowScheduleFirePreviewDstAdjustment] = {
    "shifted_forward",
}


def check_workflow_schedule_fire_preview_dst_adjustment(value: str) -> WorkflowScheduleFirePreviewDstAdjustment:
    if value in WORKFLOW_SCHEDULE_FIRE_PREVIEW_DST_ADJUSTMENT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_SCHEDULE_FIRE_PREVIEW_DST_ADJUSTMENT_VALUES!r}"
    )
