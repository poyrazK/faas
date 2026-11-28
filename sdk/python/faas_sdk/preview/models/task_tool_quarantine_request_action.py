from typing import Literal

TaskToolQuarantineRequestAction = Literal["claim", "observe"]

TASK_TOOL_QUARANTINE_REQUEST_ACTION_VALUES: set[TaskToolQuarantineRequestAction] = {
    "claim",
    "observe",
}


def check_task_tool_quarantine_request_action(value: str) -> TaskToolQuarantineRequestAction:
    if value in TASK_TOOL_QUARANTINE_REQUEST_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TASK_TOOL_QUARANTINE_REQUEST_ACTION_VALUES!r}")
