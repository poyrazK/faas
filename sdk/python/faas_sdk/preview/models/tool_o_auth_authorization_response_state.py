from typing import Literal

ToolOAuthAuthorizationResponseState = Literal[
    "authorization_denied",
    "authorization_pending",
    "authorization_uncertain",
    "connected",
    "not_started",
    "reconnect_required",
]

TOOL_O_AUTH_AUTHORIZATION_RESPONSE_STATE_VALUES: set[ToolOAuthAuthorizationResponseState] = {
    "authorization_denied",
    "authorization_pending",
    "authorization_uncertain",
    "connected",
    "not_started",
    "reconnect_required",
}


def check_tool_o_auth_authorization_response_state(value: str) -> ToolOAuthAuthorizationResponseState:
    if value in TOOL_O_AUTH_AUTHORIZATION_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TOOL_O_AUTH_AUTHORIZATION_RESPONSE_STATE_VALUES!r}")
