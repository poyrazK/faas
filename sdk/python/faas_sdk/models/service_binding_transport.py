from typing import Literal

ServiceBindingTransport = Literal["http", "https"]

SERVICE_BINDING_TRANSPORT_VALUES: set[ServiceBindingTransport] = {
    "http",
    "https",
}


def check_service_binding_transport(value: str) -> ServiceBindingTransport:
    if value in SERVICE_BINDING_TRANSPORT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SERVICE_BINDING_TRANSPORT_VALUES!r}")
