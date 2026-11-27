from typing import Literal

OutboundAIContentPolicySecret = Literal["", "block", "off", "redact"]

OUTBOUND_AI_CONTENT_POLICY_SECRET_VALUES: set[OutboundAIContentPolicySecret] = {
    "",
    "block",
    "off",
    "redact",
}


def check_outbound_ai_content_policy_secret(value: str) -> OutboundAIContentPolicySecret:
    if value in OUTBOUND_AI_CONTENT_POLICY_SECRET_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_CONTENT_POLICY_SECRET_VALUES!r}")
