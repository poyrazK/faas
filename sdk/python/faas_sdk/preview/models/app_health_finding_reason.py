from typing import Literal

AppHealthFindingReason = Literal[
    "node_evidence_missing",
    "node_heartbeat_stale",
    "node_recovering",
    "node_timestamp_invalid",
    "node_unavailable",
    "readiness_configuration_unavailable",
    "readiness_missing",
    "readiness_timestamp_invalid",
    "required_probe_unready",
]

APP_HEALTH_FINDING_REASON_VALUES: set[AppHealthFindingReason] = {
    "node_evidence_missing",
    "node_heartbeat_stale",
    "node_recovering",
    "node_timestamp_invalid",
    "node_unavailable",
    "readiness_configuration_unavailable",
    "readiness_missing",
    "readiness_timestamp_invalid",
    "required_probe_unready",
}


def check_app_health_finding_reason(value: str) -> AppHealthFindingReason:
    if value in APP_HEALTH_FINDING_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_HEALTH_FINDING_REASON_VALUES!r}")
