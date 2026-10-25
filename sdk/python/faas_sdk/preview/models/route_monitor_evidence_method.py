from typing import Literal

RouteMonitorEvidenceMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]

ROUTE_MONITOR_EVIDENCE_METHOD_VALUES: set[RouteMonitorEvidenceMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
}


def check_route_monitor_evidence_method(value: str) -> RouteMonitorEvidenceMethod:
    if value in ROUTE_MONITOR_EVIDENCE_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_EVIDENCE_METHOD_VALUES!r}")
