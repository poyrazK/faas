from typing import Literal

ToolOAuthAuthorizationRequestAction = Literal["begin", "complete"]

TOOL_O_AUTH_AUTHORIZATION_REQUEST_ACTION_VALUES: set[ToolOAuthAuthorizationRequestAction] = {
    "begin",
    "complete",
}


def check_tool_o_auth_authorization_request_action(value: str) -> ToolOAuthAuthorizationRequestAction:
    if value in TOOL_O_AUTH_AUTHORIZATION_REQUEST_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TOOL_O_AUTH_AUTHORIZATION_REQUEST_ACTION_VALUES!r}")
