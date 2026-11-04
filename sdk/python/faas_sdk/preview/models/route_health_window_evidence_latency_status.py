from typing import Literal

RouteHealthWindowEvidenceLatencyStatus = Literal["healthy", "regressed", "unknown"]

ROUTE_HEALTH_WINDOW_EVIDENCE_LATENCY_STATUS_VALUES: set[RouteHealthWindowEvidenceLatencyStatus] = {
    "healthy",
    "regressed",
    "unknown",
}


def check_route_health_window_evidence_latency_status(value: str) -> RouteHealthWindowEvidenceLatencyStatus:
    if value in ROUTE_HEALTH_WINDOW_EVIDENCE_LATENCY_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_WINDOW_EVIDENCE_LATENCY_STATUS_VALUES!r}"
    )
