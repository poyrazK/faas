from typing import Literal

AppProtectionItemStatusReason = Literal[
    "email_unverified",
    "limit_reached",
    "name_taken",
    "needs_channel",
    "needs_check",
    "needs_slo",
    "not_released",
    "plan",
    "probes_unavailable",
]

APP_PROTECTION_ITEM_STATUS_REASON_VALUES: set[AppProtectionItemStatusReason] = {
    "email_unverified",
    "limit_reached",
    "name_taken",
    "needs_channel",
    "needs_check",
    "needs_slo",
    "not_released",
    "plan",
    "probes_unavailable",
}


def check_app_protection_item_status_reason(value: str) -> AppProtectionItemStatusReason:
    if value in APP_PROTECTION_ITEM_STATUS_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_PROTECTION_ITEM_STATUS_REASON_VALUES!r}")
