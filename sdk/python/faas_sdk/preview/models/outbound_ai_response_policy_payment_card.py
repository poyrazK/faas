from typing import Literal

OutboundAIResponsePolicyPaymentCard = Literal["", "block", "off", "redact"]

OUTBOUND_AI_RESPONSE_POLICY_PAYMENT_CARD_VALUES: set[OutboundAIResponsePolicyPaymentCard] = {
    "",
    "block",
    "off",
    "redact",
}


def check_outbound_ai_response_policy_payment_card(value: str) -> OutboundAIResponsePolicyPaymentCard:
    if value in OUTBOUND_AI_RESPONSE_POLICY_PAYMENT_CARD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_RESPONSE_POLICY_PAYMENT_CARD_VALUES!r}")
