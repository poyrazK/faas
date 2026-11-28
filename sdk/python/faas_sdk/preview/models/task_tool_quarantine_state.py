from typing import Literal

TaskToolQuarantineState = Literal["applying", "qualified", "quarantined"]

TASK_TOOL_QUARANTINE_STATE_VALUES: set[TaskToolQuarantineState] = {
    "applying",
    "qualified",
    "quarantined",
}


def check_task_tool_quarantine_state(value: str) -> TaskToolQuarantineState:
    if value in TASK_TOOL_QUARANTINE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TASK_TOOL_QUARANTINE_STATE_VALUES!r}")
