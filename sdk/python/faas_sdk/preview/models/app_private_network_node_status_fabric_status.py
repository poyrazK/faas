from typing import Literal

AppPrivateNetworkNodeStatusFabricStatus = Literal["error", "ready"]

APP_PRIVATE_NETWORK_NODE_STATUS_FABRIC_STATUS_VALUES: set[AppPrivateNetworkNodeStatusFabricStatus] = {
    "error",
    "ready",
}


def check_app_private_network_node_status_fabric_status(value: str) -> AppPrivateNetworkNodeStatusFabricStatus:
    if value in APP_PRIVATE_NETWORK_NODE_STATUS_FABRIC_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APP_PRIVATE_NETWORK_NODE_STATUS_FABRIC_STATUS_VALUES!r}"
    )
