from typing import Literal

TaskToolReleaseExecutionFenceVersion = Literal[1]

TASK_TOOL_RELEASE_EXECUTION_FENCE_VERSION_VALUES: set[TaskToolReleaseExecutionFenceVersion] = {
    1,
}


def check_task_tool_release_execution_fence_version(value: int) -> TaskToolReleaseExecutionFenceVersion:
    if value in TASK_TOOL_RELEASE_EXECUTION_FENCE_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TASK_TOOL_RELEASE_EXECUTION_FENCE_VERSION_VALUES!r}")
