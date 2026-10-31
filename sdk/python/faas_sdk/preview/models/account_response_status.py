from typing import Literal

AccountResponseStatus = Literal["active", "deleted_pending", "past_due", "suspended"]

ACCOUNT_RESPONSE_STATUS_VALUES: set[AccountResponseStatus] = {
    "active",
    "deleted_pending",
    "past_due",
    "suspended",
}


def check_account_response_status(value: str) -> AccountResponseStatus:
    if value in ACCOUNT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ACCOUNT_RESPONSE_STATUS_VALUES!r}")
