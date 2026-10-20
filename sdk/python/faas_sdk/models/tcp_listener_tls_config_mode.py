from typing import Literal

TCPListenerTLSConfigMode = Literal["passthrough", "terminate"]

TCP_LISTENER_TLS_CONFIG_MODE_VALUES: set[TCPListenerTLSConfigMode] = {
    "passthrough",
    "terminate",
}


def check_tcp_listener_tls_config_mode(value: str) -> TCPListenerTLSConfigMode:
    if value in TCP_LISTENER_TLS_CONFIG_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TCP_LISTENER_TLS_CONFIG_MODE_VALUES!r}")
