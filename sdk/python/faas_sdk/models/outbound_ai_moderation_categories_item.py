from typing import Literal

OutboundAIModerationCategoriesItem = Literal[
    "harassment", "hate", "illicit", "prompt_injection", "self_harm", "sexual", "violence"
]

OUTBOUND_AI_MODERATION_CATEGORIES_ITEM_VALUES: set[OutboundAIModerationCategoriesItem] = {
    "harassment",
    "hate",
    "illicit",
    "prompt_injection",
    "self_harm",
    "sexual",
    "violence",
}


def check_outbound_ai_moderation_categories_item(value: str) -> OutboundAIModerationCategoriesItem:
    if value in OUTBOUND_AI_MODERATION_CATEGORIES_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_MODERATION_CATEGORIES_ITEM_VALUES!r}")
