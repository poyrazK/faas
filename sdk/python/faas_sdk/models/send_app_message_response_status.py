from typing import Literal

SendAppMessageResponseStatus = Literal["pending"]

SEND_APP_MESSAGE_RESPONSE_STATUS_VALUES: set[SendAppMessageResponseStatus] = {
    "pending",
}


def check_send_app_message_response_status(value: str) -> SendAppMessageResponseStatus:
    if value in SEND_APP_MESSAGE_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SEND_APP_MESSAGE_RESPONSE_STATUS_VALUES!r}")
