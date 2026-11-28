from typing import Literal

AppProtectionItemStatusKind = Literal["alert_rule", "notification_channel", "release_gate", "slo", "synthetic_check"]

APP_PROTECTION_ITEM_STATUS_KIND_VALUES: set[AppProtectionItemStatusKind] = {
    "alert_rule",
    "notification_channel",
    "release_gate",
    "slo",
    "synthetic_check",
}


def check_app_protection_item_status_kind(value: str) -> AppProtectionItemStatusKind:
    if value in APP_PROTECTION_ITEM_STATUS_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_PROTECTION_ITEM_STATUS_KIND_VALUES!r}")
