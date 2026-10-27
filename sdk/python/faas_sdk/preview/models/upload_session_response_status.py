from typing import Literal

UploadSessionResponseStatus = Literal["cancelled", "committed", "expired", "open"]

UPLOAD_SESSION_RESPONSE_STATUS_VALUES: set[UploadSessionResponseStatus] = {
    "cancelled",
    "committed",
    "expired",
    "open",
}


def check_upload_session_response_status(value: str) -> UploadSessionResponseStatus:
    if value in UPLOAD_SESSION_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UPLOAD_SESSION_RESPONSE_STATUS_VALUES!r}")
