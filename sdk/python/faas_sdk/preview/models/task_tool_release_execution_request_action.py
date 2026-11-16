from typing import Literal

TaskToolReleaseExecutionRequestAction = Literal[
    "authorize", "claim", "complete", "executor_exited", "guard", "reconcile"
]

TASK_TOOL_RELEASE_EXECUTION_REQUEST_ACTION_VALUES: set[TaskToolReleaseExecutionRequestAction] = {
    "authorize",
    "claim",
    "complete",
    "executor_exited",
    "guard",
    "reconcile",
}


def check_task_tool_release_execution_request_action(value: str) -> TaskToolReleaseExecutionRequestAction:
    if value in TASK_TOOL_RELEASE_EXECUTION_REQUEST_ACTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {TASK_TOOL_RELEASE_EXECUTION_REQUEST_ACTION_VALUES!r}"
    )
