from typing import Literal

OutboundAIResponsePolicyEmail = Literal["", "block", "off", "redact"]

OUTBOUND_AI_RESPONSE_POLICY_EMAIL_VALUES: set[OutboundAIResponsePolicyEmail] = {
    "",
    "block",
    "off",
    "redact",
}


def check_outbound_ai_response_policy_email(value: str) -> OutboundAIResponsePolicyEmail:
    if value in OUTBOUND_AI_RESPONSE_POLICY_EMAIL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_RESPONSE_POLICY_EMAIL_VALUES!r}")
