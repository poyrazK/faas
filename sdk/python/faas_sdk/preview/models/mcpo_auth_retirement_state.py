from typing import Literal

MCPOAuthRetirementState = Literal[
    "awaiting_issuance",
    "expired",
    "held",
    "installed",
    "manual_required",
    "manually_acknowledged",
    "pending",
    "provider_acknowledged",
]

MCPO_AUTH_RETIREMENT_STATE_VALUES: set[MCPOAuthRetirementState] = {
    "awaiting_issuance",
    "expired",
    "held",
    "installed",
    "manual_required",
    "manually_acknowledged",
    "pending",
    "provider_acknowledged",
}


def check_mcpo_auth_retirement_state(value: str) -> MCPOAuthRetirementState:
    if value in MCPO_AUTH_RETIREMENT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCPO_AUTH_RETIREMENT_STATE_VALUES!r}")
