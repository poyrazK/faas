from typing import Literal

OutboundAIContentPolicyEmail = Literal["", "block", "off", "redact"]

OUTBOUND_AI_CONTENT_POLICY_EMAIL_VALUES: set[OutboundAIContentPolicyEmail] = {
    "",
    "block",
    "off",
    "redact",
}


def check_outbound_ai_content_policy_email(value: str) -> OutboundAIContentPolicyEmail:
    if value in OUTBOUND_AI_CONTENT_POLICY_EMAIL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_CONTENT_POLICY_EMAIL_VALUES!r}")
