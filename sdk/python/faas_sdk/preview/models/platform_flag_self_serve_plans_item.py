from typing import Literal

PlatformFlagSelfServePlansItem = Literal["free", "hobby", "pro", "scale"]

PLATFORM_FLAG_SELF_SERVE_PLANS_ITEM_VALUES: set[PlatformFlagSelfServePlansItem] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_platform_flag_self_serve_plans_item(value: str) -> PlatformFlagSelfServePlansItem:
    if value in PLATFORM_FLAG_SELF_SERVE_PLANS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLATFORM_FLAG_SELF_SERVE_PLANS_ITEM_VALUES!r}")
