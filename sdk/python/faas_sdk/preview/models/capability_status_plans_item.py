from typing import Literal

CapabilityStatusPlansItem = Literal["free", "hobby", "pro", "scale"]

CAPABILITY_STATUS_PLANS_ITEM_VALUES: set[CapabilityStatusPlansItem] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_capability_status_plans_item(value: str) -> CapabilityStatusPlansItem:
    if value in CAPABILITY_STATUS_PLANS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CAPABILITY_STATUS_PLANS_ITEM_VALUES!r}")
