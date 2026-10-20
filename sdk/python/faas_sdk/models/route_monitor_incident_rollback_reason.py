from typing import Literal

RouteMonitorIncidentRollbackReason = Literal[
    "latency_only_violation", "no_healthy_baseline", "outside_rollback_window", "rollback_target_ineligible"
]

ROUTE_MONITOR_INCIDENT_ROLLBACK_REASON_VALUES: set[RouteMonitorIncidentRollbackReason] = {
    "latency_only_violation",
    "no_healthy_baseline",
    "outside_rollback_window",
    "rollback_target_ineligible",
}


def check_route_monitor_incident_rollback_reason(value: str) -> RouteMonitorIncidentRollbackReason:
    if value in ROUTE_MONITOR_INCIDENT_ROLLBACK_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_INCIDENT_ROLLBACK_REASON_VALUES!r}")
