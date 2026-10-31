from typing import Literal

OperatorPlatformFlagAttributesItem = Literal["account", "org", "plan"]

OPERATOR_PLATFORM_FLAG_ATTRIBUTES_ITEM_VALUES: set[OperatorPlatformFlagAttributesItem] = {
    "account",
    "org",
    "plan",
}


def check_operator_platform_flag_attributes_item(value: str) -> OperatorPlatformFlagAttributesItem:
    if value in OPERATOR_PLATFORM_FLAG_ATTRIBUTES_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATOR_PLATFORM_FLAG_ATTRIBUTES_ITEM_VALUES!r}")
