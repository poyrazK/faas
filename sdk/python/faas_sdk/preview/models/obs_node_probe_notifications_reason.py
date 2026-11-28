from typing import Literal

ObsNodeProbeNotificationsReason = Literal[
    "incomplete_evidence",
    "inventory_not_configured",
    "inventory_unavailable",
    "node_not_expected",
    "prometheus_not_configured",
]

OBS_NODE_PROBE_NOTIFICATIONS_REASON_VALUES: set[ObsNodeProbeNotificationsReason] = {
    "incomplete_evidence",
    "inventory_not_configured",
    "inventory_unavailable",
    "node_not_expected",
    "prometheus_not_configured",
}


def check_obs_node_probe_notifications_reason(value: str) -> ObsNodeProbeNotificationsReason:
    if value in OBS_NODE_PROBE_NOTIFICATIONS_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_NODE_PROBE_NOTIFICATIONS_REASON_VALUES!r}")
