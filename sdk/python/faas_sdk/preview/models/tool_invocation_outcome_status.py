from typing import Literal

ToolInvocationOutcomeStatus = Literal["completed", "not_started", "outcome_unknown", "tool_error"]

TOOL_INVOCATION_OUTCOME_STATUS_VALUES: set[ToolInvocationOutcomeStatus] = {
    "completed",
    "not_started",
    "outcome_unknown",
    "tool_error",
}


def check_tool_invocation_outcome_status(value: str) -> ToolInvocationOutcomeStatus:
    if value in TOOL_INVOCATION_OUTCOME_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TOOL_INVOCATION_OUTCOME_STATUS_VALUES!r}")
