from typing import Literal

OutboundAIContentPolicyIban = Literal["", "block", "off", "redact"]

OUTBOUND_AI_CONTENT_POLICY_IBAN_VALUES: set[OutboundAIContentPolicyIban] = {
    "",
    "block",
    "off",
    "redact",
}


def check_outbound_ai_content_policy_iban(value: str) -> OutboundAIContentPolicyIban:
    if value in OUTBOUND_AI_CONTENT_POLICY_IBAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_CONTENT_POLICY_IBAN_VALUES!r}")
