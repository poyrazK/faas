from typing import Literal

TaskToolReleaseExecutionRequestStage = Literal[
    "complete",
    "deploying_web",
    "deploying_worker",
    "draining_previous",
    "prepared",
    "previous_parked",
    "replacements_started",
    "web_promoted",
]

TASK_TOOL_RELEASE_EXECUTION_REQUEST_STAGE_VALUES: set[TaskToolReleaseExecutionRequestStage] = {
    "complete",
    "deploying_web",
    "deploying_worker",
    "draining_previous",
    "prepared",
    "previous_parked",
    "replacements_started",
    "web_promoted",
}


def check_task_tool_release_execution_request_stage(value: str) -> TaskToolReleaseExecutionRequestStage:
    if value in TASK_TOOL_RELEASE_EXECUTION_REQUEST_STAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TASK_TOOL_RELEASE_EXECUTION_REQUEST_STAGE_VALUES!r}")
