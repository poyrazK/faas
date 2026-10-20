from typing import Literal

DevBridgeActivityConnectionState = Literal["connected", "disconnected", "expired", "revoked", "unknown"]

DEV_BRIDGE_ACTIVITY_CONNECTION_STATE_VALUES: set[DevBridgeActivityConnectionState] = {
    "connected",
    "disconnected",
    "expired",
    "revoked",
    "unknown",
}


def check_dev_bridge_activity_connection_state(value: str) -> DevBridgeActivityConnectionState:
    if value in DEV_BRIDGE_ACTIVITY_CONNECTION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEV_BRIDGE_ACTIVITY_CONNECTION_STATE_VALUES!r}")
