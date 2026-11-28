from typing import Literal

TaskToolRetirementState = Literal["qualified", "retired", "retirement_pending"]

TASK_TOOL_RETIREMENT_STATE_VALUES: set[TaskToolRetirementState] = {
    "qualified",
    "retired",
    "retirement_pending",
}


def check_task_tool_retirement_state(value: str) -> TaskToolRetirementState:
    if value in TASK_TOOL_RETIREMENT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TASK_TOOL_RETIREMENT_STATE_VALUES!r}")
