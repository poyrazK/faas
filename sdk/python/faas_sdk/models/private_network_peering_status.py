from typing import Literal

PrivateNetworkPeeringStatus = Literal["error", "pending", "ready"]

PRIVATE_NETWORK_PEERING_STATUS_VALUES: set[PrivateNetworkPeeringStatus] = {
    "error",
    "pending",
    "ready",
}


def check_private_network_peering_status(value: str) -> PrivateNetworkPeeringStatus:
    if value in PRIVATE_NETWORK_PEERING_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PRIVATE_NETWORK_PEERING_STATUS_VALUES!r}")
