from typing import Literal

WorkflowScheduleDSTBehaviorFallFold = Literal["repeat_as_clock_falls_back", "run_once_at_first_occurrence"]

WORKFLOW_SCHEDULE_DST_BEHAVIOR_FALL_FOLD_VALUES: set[WorkflowScheduleDSTBehaviorFallFold] = {
    "repeat_as_clock_falls_back",
    "run_once_at_first_occurrence",
}


def check_workflow_schedule_dst_behavior_fall_fold(value: str) -> WorkflowScheduleDSTBehaviorFallFold:
    if value in WORKFLOW_SCHEDULE_DST_BEHAVIOR_FALL_FOLD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_SCHEDULE_DST_BEHAVIOR_FALL_FOLD_VALUES!r}")
