from typing import Literal

OutboundAIResponsePolicyIban = Literal["", "block", "off", "redact"]

OUTBOUND_AI_RESPONSE_POLICY_IBAN_VALUES: set[OutboundAIResponsePolicyIban] = {
    "",
    "block",
    "off",
    "redact",
}


def check_outbound_ai_response_policy_iban(value: str) -> OutboundAIResponsePolicyIban:
    if value in OUTBOUND_AI_RESPONSE_POLICY_IBAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_RESPONSE_POLICY_IBAN_VALUES!r}")
