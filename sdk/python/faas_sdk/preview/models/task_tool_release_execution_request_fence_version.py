from typing import Literal

TaskToolReleaseExecutionRequestFenceVersion = Literal[1]

TASK_TOOL_RELEASE_EXECUTION_REQUEST_FENCE_VERSION_VALUES: set[TaskToolReleaseExecutionRequestFenceVersion] = {
    1,
}


def check_task_tool_release_execution_request_fence_version(value: int) -> TaskToolReleaseExecutionRequestFenceVersion:
    if value in TASK_TOOL_RELEASE_EXECUTION_REQUEST_FENCE_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {TASK_TOOL_RELEASE_EXECUTION_REQUEST_FENCE_VERSION_VALUES!r}"
    )
