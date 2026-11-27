from typing import Literal

OutboundAIModerationAction = Literal["", "block", "flag", "off"]

OUTBOUND_AI_MODERATION_ACTION_VALUES: set[OutboundAIModerationAction] = {
    "",
    "block",
    "flag",
    "off",
}


def check_outbound_ai_moderation_action(value: str) -> OutboundAIModerationAction:
    if value in OUTBOUND_AI_MODERATION_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_MODERATION_ACTION_VALUES!r}")
