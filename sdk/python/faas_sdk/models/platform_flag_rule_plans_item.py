from typing import Literal

PlatformFlagRulePlansItem = Literal["free", "hobby", "pro", "scale"]

PLATFORM_FLAG_RULE_PLANS_ITEM_VALUES: set[PlatformFlagRulePlansItem] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_platform_flag_rule_plans_item(value: str) -> PlatformFlagRulePlansItem:
    if value in PLATFORM_FLAG_RULE_PLANS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLATFORM_FLAG_RULE_PLANS_ITEM_VALUES!r}")
