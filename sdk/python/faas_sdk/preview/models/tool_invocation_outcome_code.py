from typing import Literal

ToolInvocationOutcomeCode = Literal[
    "cancel_requested",
    "catalog_changed",
    "preflight_failed",
    "result_received",
    "timeout",
    "transport_error",
    "version_unavailable",
    "worker_interrupted",
]

TOOL_INVOCATION_OUTCOME_CODE_VALUES: set[ToolInvocationOutcomeCode] = {
    "cancel_requested",
    "catalog_changed",
    "preflight_failed",
    "result_received",
    "timeout",
    "transport_error",
    "version_unavailable",
    "worker_interrupted",
}


def check_tool_invocation_outcome_code(value: str) -> ToolInvocationOutcomeCode:
    if value in TOOL_INVOCATION_OUTCOME_CODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TOOL_INVOCATION_OUTCOME_CODE_VALUES!r}")
