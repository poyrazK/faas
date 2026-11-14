from typing import Literal

ChangeToolInvocationAction = Literal["cancel", "dispatch", "outcome"]

CHANGE_TOOL_INVOCATION_ACTION_VALUES: set[ChangeToolInvocationAction] = {
    "cancel",
    "dispatch",
    "outcome",
}


def check_change_tool_invocation_action(value: str) -> ChangeToolInvocationAction:
    if value in CHANGE_TOOL_INVOCATION_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CHANGE_TOOL_INVOCATION_ACTION_VALUES!r}")
