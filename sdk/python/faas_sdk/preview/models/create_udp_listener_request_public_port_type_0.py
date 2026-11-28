from typing import Literal

CreateUDPListenerRequestPublicPortType0 = Literal[0]

CREATE_UDP_LISTENER_REQUEST_PUBLIC_PORT_TYPE_0_VALUES: set[CreateUDPListenerRequestPublicPortType0] = {
    0,
}


def check_create_udp_listener_request_public_port_type_0(value: int) -> CreateUDPListenerRequestPublicPortType0:
    if value in CREATE_UDP_LISTENER_REQUEST_PUBLIC_PORT_TYPE_0_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_UDP_LISTENER_REQUEST_PUBLIC_PORT_TYPE_0_VALUES!r}"
    )
