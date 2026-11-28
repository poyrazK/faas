from typing import Literal

WorkflowScheduleDSTBehaviorMode = Literal["fixed_wall_time", "interval"]

WORKFLOW_SCHEDULE_DST_BEHAVIOR_MODE_VALUES: set[WorkflowScheduleDSTBehaviorMode] = {
    "fixed_wall_time",
    "interval",
}


def check_workflow_schedule_dst_behavior_mode(value: str) -> WorkflowScheduleDSTBehaviorMode:
    if value in WORKFLOW_SCHEDULE_DST_BEHAVIOR_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_SCHEDULE_DST_BEHAVIOR_MODE_VALUES!r}")
