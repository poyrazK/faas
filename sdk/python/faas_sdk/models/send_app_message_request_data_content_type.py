from typing import Literal

SendAppMessageRequestDataContentType = Literal["application/json"]

SEND_APP_MESSAGE_REQUEST_DATA_CONTENT_TYPE_VALUES: set[SendAppMessageRequestDataContentType] = {
    "application/json",
}


def check_send_app_message_request_data_content_type(value: str) -> SendAppMessageRequestDataContentType:
    if value in SEND_APP_MESSAGE_REQUEST_DATA_CONTENT_TYPE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SEND_APP_MESSAGE_REQUEST_DATA_CONTENT_TYPE_VALUES!r}"
    )
