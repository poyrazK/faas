from typing import Literal

PrivateNetworkStatus = Literal["error", "ready"]

PRIVATE_NETWORK_STATUS_VALUES: set[PrivateNetworkStatus] = {
    "error",
    "ready",
}


def check_private_network_status(value: str) -> PrivateNetworkStatus:
    if value in PRIVATE_NETWORK_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PRIVATE_NETWORK_STATUS_VALUES!r}")
