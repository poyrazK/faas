from typing import Literal

ToolOAuthConnectionStatusRecoveryAction = Literal["reconnect", "review_connection"]

TOOL_O_AUTH_CONNECTION_STATUS_RECOVERY_ACTION_VALUES: set[ToolOAuthConnectionStatusRecoveryAction] = {
    "reconnect",
    "review_connection",
}


def check_tool_o_auth_connection_status_recovery_action(value: str) -> ToolOAuthConnectionStatusRecoveryAction:
    if value in TOOL_O_AUTH_CONNECTION_STATUS_RECOVERY_ACTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {TOOL_O_AUTH_CONNECTION_STATUS_RECOVERY_ACTION_VALUES!r}"
    )
