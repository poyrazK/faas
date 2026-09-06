from typing import Literal

UpsertDevSessionRequestType = Literal["app", "function"]

UPSERT_DEV_SESSION_REQUEST_TYPE_VALUES: set[UpsertDevSessionRequestType] = {
    "app",
    "function",
}


def check_upsert_dev_session_request_type(value: str) -> UpsertDevSessionRequestType:
    if value in UPSERT_DEV_SESSION_REQUEST_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UPSERT_DEV_SESSION_REQUEST_TYPE_VALUES!r}")
