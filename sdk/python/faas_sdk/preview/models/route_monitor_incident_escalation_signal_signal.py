from typing import Literal

RouteMonitorIncidentEscalationSignalSignal = Literal["errors", "latency"]

ROUTE_MONITOR_INCIDENT_ESCALATION_SIGNAL_SIGNAL_VALUES: set[RouteMonitorIncidentEscalationSignalSignal] = {
    "errors",
    "latency",
}


def check_route_monitor_incident_escalation_signal_signal(value: str) -> RouteMonitorIncidentEscalationSignalSignal:
    if value in ROUTE_MONITOR_INCIDENT_ESCALATION_SIGNAL_SIGNAL_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_INCIDENT_ESCALATION_SIGNAL_SIGNAL_VALUES!r}"
    )
