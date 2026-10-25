from typing import Literal

TCPListenerTLSCertificateStatusStatus = Literal["not_ready", "ready", "unknown"]

TCP_LISTENER_TLS_CERTIFICATE_STATUS_STATUS_VALUES: set[TCPListenerTLSCertificateStatusStatus] = {
    "not_ready",
    "ready",
    "unknown",
}


def check_tcp_listener_tls_certificate_status_status(value: str) -> TCPListenerTLSCertificateStatusStatus:
    if value in TCP_LISTENER_TLS_CERTIFICATE_STATUS_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {TCP_LISTENER_TLS_CERTIFICATE_STATUS_STATUS_VALUES!r}"
    )
