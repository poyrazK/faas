from typing import Literal

OutboundAIModerationKind = Literal["classifier", "openai_moderation"]

OUTBOUND_AI_MODERATION_KIND_VALUES: set[OutboundAIModerationKind] = {
    "classifier",
    "openai_moderation",
}


def check_outbound_ai_moderation_kind(value: str) -> OutboundAIModerationKind:
    if value in OUTBOUND_AI_MODERATION_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_MODERATION_KIND_VALUES!r}")
