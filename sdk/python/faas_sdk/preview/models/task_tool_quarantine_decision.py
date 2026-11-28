from typing import Literal

TaskToolQuarantineDecision = Literal["approved", "revoked"]

TASK_TOOL_QUARANTINE_DECISION_VALUES: set[TaskToolQuarantineDecision] = {
    "approved",
    "revoked",
}


def check_task_tool_quarantine_decision(value: str) -> TaskToolQuarantineDecision:
    if value in TASK_TOOL_QUARANTINE_DECISION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TASK_TOOL_QUARANTINE_DECISION_VALUES!r}")
