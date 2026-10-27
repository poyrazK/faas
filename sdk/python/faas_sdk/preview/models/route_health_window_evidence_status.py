from typing import Literal

RouteHealthWindowEvidenceStatus = Literal["healthy", "regressed", "unknown"]

ROUTE_HEALTH_WINDOW_EVIDENCE_STATUS_VALUES: set[RouteHealthWindowEvidenceStatus] = {
    "healthy",
    "regressed",
    "unknown",
}


def check_route_health_window_evidence_status(value: str) -> RouteHealthWindowEvidenceStatus:
    if value in ROUTE_HEALTH_WINDOW_EVIDENCE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_WINDOW_EVIDENCE_STATUS_VALUES!r}")
