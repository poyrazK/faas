from typing import Literal

WorkflowScheduleDSTBehaviorSpringGap = Literal["follow_cron_interval", "shift_to_first_valid_minute"]

WORKFLOW_SCHEDULE_DST_BEHAVIOR_SPRING_GAP_VALUES: set[WorkflowScheduleDSTBehaviorSpringGap] = {
    "follow_cron_interval",
    "shift_to_first_valid_minute",
}


def check_workflow_schedule_dst_behavior_spring_gap(value: str) -> WorkflowScheduleDSTBehaviorSpringGap:
    if value in WORKFLOW_SCHEDULE_DST_BEHAVIOR_SPRING_GAP_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_SCHEDULE_DST_BEHAVIOR_SPRING_GAP_VALUES!r}")
