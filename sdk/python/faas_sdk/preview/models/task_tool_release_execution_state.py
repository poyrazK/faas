from typing import Literal

TaskToolReleaseExecutionState = Literal["authorized", "completed", "reconcile_required", "running"]

TASK_TOOL_RELEASE_EXECUTION_STATE_VALUES: set[TaskToolReleaseExecutionState] = {
    "authorized",
    "completed",
    "reconcile_required",
    "running",
}


def check_task_tool_release_execution_state(value: str) -> TaskToolReleaseExecutionState:
    if value in TASK_TOOL_RELEASE_EXECUTION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TASK_TOOL_RELEASE_EXECUTION_STATE_VALUES!r}")
