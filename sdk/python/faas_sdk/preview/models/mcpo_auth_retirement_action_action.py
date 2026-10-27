from typing import Literal

MCPOAuthRetirementActionAction = Literal["acknowledge_manual", "retry"]

MCPO_AUTH_RETIREMENT_ACTION_ACTION_VALUES: set[MCPOAuthRetirementActionAction] = {
    "acknowledge_manual",
    "retry",
}


def check_mcpo_auth_retirement_action_action(value: str) -> MCPOAuthRetirementActionAction:
    if value in MCPO_AUTH_RETIREMENT_ACTION_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCPO_AUTH_RETIREMENT_ACTION_ACTION_VALUES!r}")
