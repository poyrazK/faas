from typing import Literal

ObsNodeProbeNotificationsState = Literal["available", "not_applicable", "partial", "unknown"]

OBS_NODE_PROBE_NOTIFICATIONS_STATE_VALUES: set[ObsNodeProbeNotificationsState] = {
    "available",
    "not_applicable",
    "partial",
    "unknown",
}


def check_obs_node_probe_notifications_state(value: str) -> ObsNodeProbeNotificationsState:
    if value in OBS_NODE_PROBE_NOTIFICATIONS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_NODE_PROBE_NOTIFICATIONS_STATE_VALUES!r}")
