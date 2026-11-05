from typing import Literal

SecretEntryStatusStatus = Literal["pending", "set"]

SECRET_ENTRY_STATUS_STATUS_VALUES: set[SecretEntryStatusStatus] = {
    "pending",
    "set",
}


def check_secret_entry_status_status(value: str) -> SecretEntryStatusStatus:
    if value in SECRET_ENTRY_STATUS_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SECRET_ENTRY_STATUS_STATUS_VALUES!r}")
