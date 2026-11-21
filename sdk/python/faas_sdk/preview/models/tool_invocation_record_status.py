from typing import Literal

ToolInvocationRecordStatus = Literal[
    "cancelled", "completed", "not_started", "outcome_unknown", "queued", "running", "tool_error"
]

TOOL_INVOCATION_RECORD_STATUS_VALUES: set[ToolInvocationRecordStatus] = {
    "cancelled",
    "completed",
    "not_started",
    "outcome_unknown",
    "queued",
    "running",
    "tool_error",
}


def check_tool_invocation_record_status(value: str) -> ToolInvocationRecordStatus:
    if value in TOOL_INVOCATION_RECORD_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TOOL_INVOCATION_RECORD_STATUS_VALUES!r}")
