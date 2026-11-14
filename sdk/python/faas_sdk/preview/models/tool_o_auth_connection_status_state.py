from typing import Literal

ToolOAuthConnectionStatusState = Literal["ready", "reconnect_required", "refresh_required", "refreshing"]

TOOL_O_AUTH_CONNECTION_STATUS_STATE_VALUES: set[ToolOAuthConnectionStatusState] = {
    "ready",
    "reconnect_required",
    "refresh_required",
    "refreshing",
}


def check_tool_o_auth_connection_status_state(value: str) -> ToolOAuthConnectionStatusState:
    if value in TOOL_O_AUTH_CONNECTION_STATUS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TOOL_O_AUTH_CONNECTION_STATUS_STATE_VALUES!r}")
