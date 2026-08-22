from typing import Literal

CreateAppRequestAppProtocol = Literal["grpc", "http1", "http2"]

CREATE_APP_REQUEST_APP_PROTOCOL_VALUES: set[CreateAppRequestAppProtocol] = {
    "grpc",
    "http1",
    "http2",
}


def check_create_app_request_app_protocol(value: str) -> CreateAppRequestAppProtocol:
    if value in CREATE_APP_REQUEST_APP_PROTOCOL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_APP_REQUEST_APP_PROTOCOL_VALUES!r}")
