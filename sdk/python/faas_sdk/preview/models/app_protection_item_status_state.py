from typing import Literal

AppProtectionItemStatusState = Literal["active", "pending"]

APP_PROTECTION_ITEM_STATUS_STATE_VALUES: set[AppProtectionItemStatusState] = {
    "active",
    "pending",
}


def check_app_protection_item_status_state(value: str) -> AppProtectionItemStatusState:
    if value in APP_PROTECTION_ITEM_STATUS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_PROTECTION_ITEM_STATUS_STATE_VALUES!r}")
