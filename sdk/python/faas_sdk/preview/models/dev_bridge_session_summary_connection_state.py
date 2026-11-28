from typing import Literal

DevBridgeSessionSummaryConnectionState = Literal["connected", "disconnected", "expired", "revoked", "unknown"]

DEV_BRIDGE_SESSION_SUMMARY_CONNECTION_STATE_VALUES: set[DevBridgeSessionSummaryConnectionState] = {
    "connected",
    "disconnected",
    "expired",
    "revoked",
    "unknown",
}


def check_dev_bridge_session_summary_connection_state(value: str) -> DevBridgeSessionSummaryConnectionState:
    if value in DEV_BRIDGE_SESSION_SUMMARY_CONNECTION_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {DEV_BRIDGE_SESSION_SUMMARY_CONNECTION_STATE_VALUES!r}"
    )
