from typing import Literal

TCPListenerTLSStatusResponseScope = Literal["observed_edges"]

TCP_LISTENER_TLS_STATUS_RESPONSE_SCOPE_VALUES: set[TCPListenerTLSStatusResponseScope] = {
    "observed_edges",
}


def check_tcp_listener_tls_status_response_scope(value: str) -> TCPListenerTLSStatusResponseScope:
    if value in TCP_LISTENER_TLS_STATUS_RESPONSE_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TCP_LISTENER_TLS_STATUS_RESPONSE_SCOPE_VALUES!r}")
