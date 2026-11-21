from typing import Literal

OutboundAIContentPolicyPaymentCard = Literal["", "block", "off", "redact"]

OUTBOUND_AI_CONTENT_POLICY_PAYMENT_CARD_VALUES: set[OutboundAIContentPolicyPaymentCard] = {
    "",
    "block",
    "off",
    "redact",
}


def check_outbound_ai_content_policy_payment_card(value: str) -> OutboundAIContentPolicyPaymentCard:
    if value in OUTBOUND_AI_CONTENT_POLICY_PAYMENT_CARD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_CONTENT_POLICY_PAYMENT_CARD_VALUES!r}")
