from typing import Literal

AccountDeletionResponseStatus = Literal["deleted_pending"]

ACCOUNT_DELETION_RESPONSE_STATUS_VALUES: set[AccountDeletionResponseStatus] = {
    "deleted_pending",
}


def check_account_deletion_response_status(value: str) -> AccountDeletionResponseStatus:
    if value in ACCOUNT_DELETION_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ACCOUNT_DELETION_RESPONSE_STATUS_VALUES!r}")
