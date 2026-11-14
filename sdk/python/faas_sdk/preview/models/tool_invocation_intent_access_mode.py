from typing import Literal

ToolInvocationIntentAccessMode = Literal["external-oauth", "open"]

TOOL_INVOCATION_INTENT_ACCESS_MODE_VALUES: set[ToolInvocationIntentAccessMode] = {
    "external-oauth",
    "open",
}


def check_tool_invocation_intent_access_mode(value: str) -> ToolInvocationIntentAccessMode:
    if value in TOOL_INVOCATION_INTENT_ACCESS_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TOOL_INVOCATION_INTENT_ACCESS_MODE_VALUES!r}")
