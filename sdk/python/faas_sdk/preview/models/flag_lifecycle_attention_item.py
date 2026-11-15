from typing import Literal

FlagLifecycleAttentionItem = Literal["expired", "expires_soon", "never_evaluated", "no_owner", "stale"]

FLAG_LIFECYCLE_ATTENTION_ITEM_VALUES: set[FlagLifecycleAttentionItem] = {
    "expired",
    "expires_soon",
    "never_evaluated",
    "no_owner",
    "stale",
}


def check_flag_lifecycle_attention_item(value: str) -> FlagLifecycleAttentionItem:
    if value in FLAG_LIFECYCLE_ATTENTION_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_LIFECYCLE_ATTENTION_ITEM_VALUES!r}")
