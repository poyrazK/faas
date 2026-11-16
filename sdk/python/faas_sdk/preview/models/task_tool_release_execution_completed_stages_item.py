from typing import Literal

TaskToolReleaseExecutionCompletedStagesItem = Literal[
    "baseline_captured",
    "previous_workers_parked",
    "promotion_recorded",
    "release_completed",
    "web_submitted",
    "worker_submitted",
    "workers_recorded",
]

TASK_TOOL_RELEASE_EXECUTION_COMPLETED_STAGES_ITEM_VALUES: set[TaskToolReleaseExecutionCompletedStagesItem] = {
    "baseline_captured",
    "previous_workers_parked",
    "promotion_recorded",
    "release_completed",
    "web_submitted",
    "worker_submitted",
    "workers_recorded",
}


def check_task_tool_release_execution_completed_stages_item(value: str) -> TaskToolReleaseExecutionCompletedStagesItem:
    if value in TASK_TOOL_RELEASE_EXECUTION_COMPLETED_STAGES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {TASK_TOOL_RELEASE_EXECUTION_COMPLETED_STAGES_ITEM_VALUES!r}"
    )
