from typing import Literal

TCPListenerResponseProtocol = Literal["tcp"]

TCP_LISTENER_RESPONSE_PROTOCOL_VALUES: set[TCPListenerResponseProtocol] = {
    "tcp",
}


def check_tcp_listener_response_protocol(value: str) -> TCPListenerResponseProtocol:
    if value in TCP_LISTENER_RESPONSE_PROTOCOL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TCP_LISTENER_RESPONSE_PROTOCOL_VALUES!r}")
