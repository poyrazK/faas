from typing import Literal

RouteMonitorEvidenceSignal = Literal["errors", "latency"]

ROUTE_MONITOR_EVIDENCE_SIGNAL_VALUES: set[RouteMonitorEvidenceSignal] = {
    "errors",
    "latency",
}


def check_route_monitor_evidence_signal(value: str) -> RouteMonitorEvidenceSignal:
    if value in ROUTE_MONITOR_EVIDENCE_SIGNAL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_EVIDENCE_SIGNAL_VALUES!r}")
