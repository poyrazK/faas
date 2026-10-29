from typing import Literal

PreAuthEnforcementSuggestionStatus = Literal["insufficient_data", "ready", "review"]

PRE_AUTH_ENFORCEMENT_SUGGESTION_STATUS_VALUES: set[PreAuthEnforcementSuggestionStatus] = {
    "insufficient_data",
    "ready",
    "review",
}


def check_pre_auth_enforcement_suggestion_status(value: str) -> PreAuthEnforcementSuggestionStatus:
    if value in PRE_AUTH_ENFORCEMENT_SUGGESTION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PRE_AUTH_ENFORCEMENT_SUGGESTION_STATUS_VALUES!r}")
