from typing import Literal

OutboundAIResponsePolicyModeration = Literal["", "block", "flag", "off"]

OUTBOUND_AI_RESPONSE_POLICY_MODERATION_VALUES: set[OutboundAIResponsePolicyModeration] = {
    "",
    "block",
    "flag",
    "off",
}


def check_outbound_ai_response_policy_moderation(value: str) -> OutboundAIResponsePolicyModeration:
    if value in OUTBOUND_AI_RESPONSE_POLICY_MODERATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_RESPONSE_POLICY_MODERATION_VALUES!r}")
