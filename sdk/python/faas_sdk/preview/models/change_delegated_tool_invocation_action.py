from typing import Literal

ChangeDelegatedToolInvocationAction = Literal["cancel", "dispatch", "outcome"]

CHANGE_DELEGATED_TOOL_INVOCATION_ACTION_VALUES: set[ChangeDelegatedToolInvocationAction] = {
    "cancel",
    "dispatch",
    "outcome",
}


def check_change_delegated_tool_invocation_action(value: str) -> ChangeDelegatedToolInvocationAction:
    if value in CHANGE_DELEGATED_TOOL_INVOCATION_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CHANGE_DELEGATED_TOOL_INVOCATION_ACTION_VALUES!r}")
