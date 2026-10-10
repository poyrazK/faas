from typing import Literal

EdgeProtectionResponseRejectionsItemStatus = Literal["401", "403", "413", "429", "503", "other"]

EDGE_PROTECTION_RESPONSE_REJECTIONS_ITEM_STATUS_VALUES: set[EdgeProtectionResponseRejectionsItemStatus] = {
    "401",
    "403",
    "413",
    "429",
    "503",
    "other",
}


def check_edge_protection_response_rejections_item_status(value: str) -> EdgeProtectionResponseRejectionsItemStatus:
    if value in EDGE_PROTECTION_RESPONSE_REJECTIONS_ITEM_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EDGE_PROTECTION_RESPONSE_REJECTIONS_ITEM_STATUS_VALUES!r}"
    )
