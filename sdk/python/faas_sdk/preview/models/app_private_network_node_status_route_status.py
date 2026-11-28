from typing import Literal

AppPrivateNetworkNodeStatusRouteStatus = Literal["error", "ready"]

APP_PRIVATE_NETWORK_NODE_STATUS_ROUTE_STATUS_VALUES: set[AppPrivateNetworkNodeStatusRouteStatus] = {
    "error",
    "ready",
}


def check_app_private_network_node_status_route_status(value: str) -> AppPrivateNetworkNodeStatusRouteStatus:
    if value in APP_PRIVATE_NETWORK_NODE_STATUS_ROUTE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APP_PRIVATE_NETWORK_NODE_STATUS_ROUTE_STATUS_VALUES!r}"
    )
