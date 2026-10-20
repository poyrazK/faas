from typing import Literal

UDPListenerResponseProtocol = Literal["udp"]

UDP_LISTENER_RESPONSE_PROTOCOL_VALUES: set[UDPListenerResponseProtocol] = {
    "udp",
}


def check_udp_listener_response_protocol(value: str) -> UDPListenerResponseProtocol:
    if value in UDP_LISTENER_RESPONSE_PROTOCOL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UDP_LISTENER_RESPONSE_PROTOCOL_VALUES!r}")
