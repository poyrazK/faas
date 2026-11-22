from typing import Literal

OutboundAIResponsePolicySecret = Literal["", "block", "off", "redact"]

OUTBOUND_AI_RESPONSE_POLICY_SECRET_VALUES: set[OutboundAIResponsePolicySecret] = {
    "",
    "block",
    "off",
    "redact",
}


def check_outbound_ai_response_policy_secret(value: str) -> OutboundAIResponsePolicySecret:
    if value in OUTBOUND_AI_RESPONSE_POLICY_SECRET_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_RESPONSE_POLICY_SECRET_VALUES!r}")
