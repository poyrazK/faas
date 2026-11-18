from typing import Literal

AppProtectionItemStatusItem = Literal[
    "availability_burn_alert",
    "availability_gate",
    "availability_slo",
    "email_channel",
    "health_alert",
    "health_check",
    "latency_burn_alert",
    "latency_slo",
]

APP_PROTECTION_ITEM_STATUS_ITEM_VALUES: set[AppProtectionItemStatusItem] = {
    "availability_burn_alert",
    "availability_gate",
    "availability_slo",
    "email_channel",
    "health_alert",
    "health_check",
    "latency_burn_alert",
    "latency_slo",
}


def check_app_protection_item_status_item(value: str) -> AppProtectionItemStatusItem:
    if value in APP_PROTECTION_ITEM_STATUS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_PROTECTION_ITEM_STATUS_ITEM_VALUES!r}")
