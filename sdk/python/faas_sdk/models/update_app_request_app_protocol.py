from typing import Literal

UpdateAppRequestAppProtocol = Literal["grpc", "http1", "http2"]

UPDATE_APP_REQUEST_APP_PROTOCOL_VALUES: set[UpdateAppRequestAppProtocol] = {
    "grpc",
    "http1",
    "http2",
}


def check_update_app_request_app_protocol(value: str) -> UpdateAppRequestAppProtocol:
    if value in UPDATE_APP_REQUEST_APP_PROTOCOL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UPDATE_APP_REQUEST_APP_PROTOCOL_VALUES!r}")
