from typing import Literal

AppResponseAppProtocol = Literal["grpc", "http1", "http2"]

APP_RESPONSE_APP_PROTOCOL_VALUES: set[AppResponseAppProtocol] = {
    "grpc",
    "http1",
    "http2",
}


def check_app_response_app_protocol(value: str) -> AppResponseAppProtocol:
    if value in APP_RESPONSE_APP_PROTOCOL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_RESPONSE_APP_PROTOCOL_VALUES!r}")
