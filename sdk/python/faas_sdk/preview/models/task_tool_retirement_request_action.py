from typing import Literal

TaskToolRetirementRequestAction = Literal["observe", "park"]

TASK_TOOL_RETIREMENT_REQUEST_ACTION_VALUES: set[TaskToolRetirementRequestAction] = {
    "observe",
    "park",
}


def check_task_tool_retirement_request_action(value: str) -> TaskToolRetirementRequestAction:
    if value in TASK_TOOL_RETIREMENT_REQUEST_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TASK_TOOL_RETIREMENT_REQUEST_ACTION_VALUES!r}")
