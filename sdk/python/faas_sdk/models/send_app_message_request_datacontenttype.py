from typing import Literal

SendAppMessageRequestDatacontenttype = Literal["application/json"]

SEND_APP_MESSAGE_REQUEST_DATACONTENTTYPE_VALUES: set[SendAppMessageRequestDatacontenttype] = {
    "application/json",
}


def check_send_app_message_request_datacontenttype(value: str) -> SendAppMessageRequestDatacontenttype:
    if value in SEND_APP_MESSAGE_REQUEST_DATACONTENTTYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SEND_APP_MESSAGE_REQUEST_DATACONTENTTYPE_VALUES!r}")
