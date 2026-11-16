from typing import Literal

TaskToolRetirementDecision = Literal["approved", "revoked"]

TASK_TOOL_RETIREMENT_DECISION_VALUES: set[TaskToolRetirementDecision] = {
    "approved",
    "revoked",
}


def check_task_tool_retirement_decision(value: str) -> TaskToolRetirementDecision:
    if value in TASK_TOOL_RETIREMENT_DECISION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TASK_TOOL_RETIREMENT_DECISION_VALUES!r}")
