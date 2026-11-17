from typing import Literal

PersonalSupportListStatus = Literal["open", "pending", "resolved"]

PERSONAL_SUPPORT_LIST_STATUS_VALUES: set[PersonalSupportListStatus] = {
    "open",
    "pending",
    "resolved",
}


def check_personal_support_list_status(value: str) -> PersonalSupportListStatus:
    if value in PERSONAL_SUPPORT_LIST_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PERSONAL_SUPPORT_LIST_STATUS_VALUES!r}")
