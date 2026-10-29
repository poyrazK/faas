from typing import Literal

OutboundAIModerationOnError = Literal["", "allow", "block"]

OUTBOUND_AI_MODERATION_ON_ERROR_VALUES: set[OutboundAIModerationOnError] = {
    "",
    "allow",
    "block",
}


def check_outbound_ai_moderation_on_error(value: str) -> OutboundAIModerationOnError:
    if value in OUTBOUND_AI_MODERATION_ON_ERROR_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_MODERATION_ON_ERROR_VALUES!r}")
