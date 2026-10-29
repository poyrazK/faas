from typing import Literal

RouteMonitorFindingEvidenceWindow = Literal["pooled"]

ROUTE_MONITOR_FINDING_EVIDENCE_WINDOW_VALUES: set[RouteMonitorFindingEvidenceWindow] = {
    "pooled",
}


def check_route_monitor_finding_evidence_window(value: str) -> RouteMonitorFindingEvidenceWindow:
    if value in ROUTE_MONITOR_FINDING_EVIDENCE_WINDOW_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_FINDING_EVIDENCE_WINDOW_VALUES!r}")
