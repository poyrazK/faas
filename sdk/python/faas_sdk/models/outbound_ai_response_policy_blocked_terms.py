from typing import Literal

OutboundAIResponsePolicyBlockedTerms = Literal["", "block", "off"]

OUTBOUND_AI_RESPONSE_POLICY_BLOCKED_TERMS_VALUES: set[OutboundAIResponsePolicyBlockedTerms] = {
    "",
    "block",
    "off",
}


def check_outbound_ai_response_policy_blocked_terms(value: str) -> OutboundAIResponsePolicyBlockedTerms:
    if value in OUTBOUND_AI_RESPONSE_POLICY_BLOCKED_TERMS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_RESPONSE_POLICY_BLOCKED_TERMS_VALUES!r}")
